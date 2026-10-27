# Design

## Context

动机见 [proposal.md](proposal.md)，行为见 [specs/framework-execution/spec.md](specs/framework-execution/spec.md)。基线实现的断点是 Runtime 把 `Options.Limits` 传给宿主 wire，而 `sdk.serve` 创建 Guest 时不传限制；`contract.Initialize` 与 `Registration` 都无预算字段。

2026-10-27 在 darwin/arm64、Go 1.27.3 的临时 SDK/Transport 诊断中，宿主设置 1024、SDK 使用默认 1 MiB，SDK 返回约 2 KiB 数据后，宿主拒收并关闭，SDK 将 EOF 归一化为 nil。另一个端点测试确认发送端 4096/接收端 1024 的同类失败。它们证明当前预算不一致，不是新方案的通过证据。

用户已明确项目未上线，直接修改当前契约及所有调用者，禁止旧字段/缺字段回退和新版本轨道。本设计不使用早期冻结 v1 字段的约束；schema 标识仍为当前值，签名算法及输入布局不涉及预算，不改动。`build-go-plugin-framework` 的单一作者声明、配置及 publish/retire 由另一 owner 规划，本 change 只接入这些生命周期，不重定义它们。

## Goals / Non-Goals

**Goals:**

- 宿主所选预算从 initialize 第一帧到 shutdown 一致，SDK 的 Start 反向调用也不得处于另一预算。
- 最小可用预算能容纳所有合法关联的固定尺寸错误和取消消息；不靠放宽接收端自救。
- 契约、编码器、SDK、启动入口、schema、fixture 和文档在一次集成中切换。

**Non-Goals:**

- 不新增 hello/ack 往返、环境变量、命令行预算传输、插件私有限额选项或运行中重新协商。
- 不协商其他 Limits 字段，不把进程/WASM 内存、stdout 总量、业务对象分配与帧上限混为一谈。
- 不增加新的公开 Scope 能力接口只为读取预算；显式 Host 配置和已确认的握手值足以定位本次问题。

## Decisions

### 1. 宿主提议、Guest 原值确认

直接增加两个必需字段：

| 位置 | Go / JSON | 含义 |
|---|---|---|
| `contract.Initialize` | `FrameBytes int` / `frameBytes`，无 omitempty | Host 选择的完整帧预算 N |
| `contract.Registration` | `FrameBytes int` / `frameBytes`，无 omitempty | Guest 已经应用的 N，必须原值确认 |

当前协议统一支持 `4096 <= N <= 1048576`。SDK 没有自己的第二个帧上限配置；不适用当前范围的 peer 明确失败。两端业务发送和接收都用 N。握手前后都不取 min，不在 registration 才改变任何一端预算。

拒绝双方分别提议再取最小值：Guest 的 Start 可以在 registration 前回调 Host，届时 Host 若尚不知道较小值，仍会发送 Guest 接收不了的结果；补救需要更多握手阶段，本需求无需增加。拒绝环境变量注入：无法统一 Transport、Native、WASI 及手工 wire 路径，容易制造第二事实源。

### 2. 唯一配置来源与直接 API 切换

Host 门面增加显式 `Options.FrameBytes`，按 activation 传入 `contract.Launch.FrameBytes`，同一个值填入 Initialize。通用外部执行在启动进程前校验范围；builtin-only 或显式自有 Adapter 不因未使用 generic 协议而被要求虚构通用预算。直接 Runtime 的通用执行调用者必须填写 Launch.FrameBytes。

删除 `wire.Limits.FrameBytes`；Limits 继续只持有 Inflight、Deadline、Grace，本 change 不改变这些值的策略。`wire.Options.FrameBytes` 接收 Host 的必需值，`DefaultLimits` 不再产生帧默认值，`New` 不把 0 补成 1 MiB。Guest 模式的 FrameBytes 只能保持未绑定的 0，直到必需 initialize 成功绑定；这表示引导状态，不是省略线上字段的默认或可用预算。Guest 未绑定时禁止主动发送任何请求。

`contract.SessionFactory` 从函数直接改为描述符 `{Protocol, Open}`：Protocol 必须明确为 `generic` 或 `custom`，Open 是原构造函数加 `frameBytes int` 参数，不保留旧函数别名。`Launch.OpenSession` 替换为 `Launch.SessionFactory *SessionFactory`，Host 的 selector 同步返回该描述符指针；nil 表示选用库内通用实现，非 nil 描述符的 Protocol/Open 缺失或未知均拒绝，不能把“存在 factory”当作自有协议。Host 的 factory selector 在运行记录/策略激活和启动前产生一次描述符，Host 与 Runtime 使用这一份选择，不重复探测或选择。

Host 和 process 在启动任何进程前按该描述符校验：generic（包括 lifecycle-smoke 等包装器）必须有合法 N；custom 不执行 generic 范围校验，其 Open/PluginSession Adapter 拥有自有策略。process 将 Launch.FrameBytes 原样交给 Open，通用 wrapper 必须以该值创建 wire。返回后仅验证已声明的能力：custom 必须提供当前 PluginSession 映射，generic 走通用路径；声明与返回不符直接失败，不重新尝试另一种协议。自有 Adapter 可忽略不属于其协议的 frameBytes，本地零值不参与协议判别。

`genericPlugin.Initialize` 仍直接序列化已构造的 Initialize。默认 wire 在发送 initialize 前严格解码并要求 `input.FrameBytes == Options.FrameBytes`，手工 Call 不能谎报。Host 门面在发布前验证 Registration 原值确认，即使 Runtime 返回的是非 wire 实现也不能提交未经确认的 generic 注册；自有 PluginSession 的映射由其 Adapter 负责，无额外通用传输。

### 3. 第一帧绑定在 readLoop 内同步完成

不在 SDK handler 开始后才改限制，因为 readLoop 可能已用旧值读取下一帧。wire 保留原始读取长度（含空白与 LF），在读循环/receive 的同步初始化准入阶段完成：

1. Guest 只有第一条 initialize 可用 1 MiB 硬上限读取，超限在完整解码前拒绝。帧身份、方向、顺序和 strict Initialize 结构都必须合法。
2. 校验 N、身份一致性和原始完整帧长度 `<= N`；不能重新 Marshal 来计算收到的大小。初始化字段缺失/重复、未知字段或不合法预算均直接失败并关闭，handler 不被调用。
3. 在启动 handler goroutine 和读取第二帧之前，一次性绑定 activation 与 N。此后不可再次 initialize，不能“失败重试”改预算。所有发送、下一帧读取及现有 bufio 缓冲都使用 N。
4. Guest 的 SDK initialize 完成协议/definition/config 校验后才调用作者 Validate/Start。SDK 返回 Registration 原值确认；它不修改 wire 的限额，只消费已经验证的初始化数据。

Host 从 New 开始即使用 N，没有引导放宽。正常 Host 的完整 initialize 自身超过 N 时，在写出前失败；调用方不能依赖 Guest 的引导上限传送超大配置。

readLoop 只在第一条帧绑定前使用引导上限；读缓冲可预取有限字节，但下一条完整帧的累计计量必须以 N 检查。绑定和读取快照遵守现有互斥规则，无运行期 setter，也不跨线程直接改 `Limits` 引入 data race。

### 4. 确认、取消及发布的线性化边界

Host 收到成功 initialize 响应时，在 readLoop 处理响应的同步阶段验证 Registration 结构及 FrameBytes 原值，再设置初始化状态和交付等待结果。这样同一读缓冲中的紧随请求不依赖 Initialize 调用者何时恢复执行。

Guest 的成功 registration 必须先完成严格校验和编码；发送准入与初始化状态提交对于接收准入是不可分割的。具体采用仅用于初始化确认的发送提交屏障：确认帧可先取得有界队列准入，但 writer 在状态提交前不能写出任何该帧字节；持锁提交 ready 后释放屏障，接收准入使用同一状态锁。不能简单先 `send` 再无保护地置位，因为 writer 可能先把确认交付，Host 随即回调而 Guest 仍判未初始化。编码/入队失败不置 ready；已观察到取消或会话关闭时走取消/失败路径，屏障等待必须随关闭结束，不能阻塞终止。该屏障不是额外线上握手，也不绕开独立发布门控。

所有初始化反向请求只依赖合法 initialize parent，必须已经绑定 N。普通根请求仍遵循初始化准入；`activation.publish`/`activation.retire` 继续按并行 change 的最终发布、单调退役与重复幂等语义执行。初始化完成不自动发布后台工作，不得让迟到 Registration 绕过取消或退役。

已绑定后的失败响应也用 N，未绑定时的坏 initialize 直接关闭并保留本地安全原因，不尝试猜测一个远端可收的错误帧。初始化失败、取消、注册表超限走当前清理/终止流程；不替换大注册表为小的成功注册，不扩预算重试 Start。fatal 后仍触发本地清理并汇总实际失败；依赖反向 RPC 的 disposer 在会话已断时可能失败，不承诺远端资源必然释放，也不吞掉运行记录清理失败。

普通响应复用 `isolate-oversized-responses` 的唯一替代路径。控制方法 initialize、health、shutdown、activation.publish、activation.retire 及初始化前反向响应仍按该 change 的致命边界处理。namespace 的 submission-unknown 和 storage recovery 区别由并行领域契约拥有，不改 wire 的迟到拒绝和不重试原则。

### 5. 4 KiB 下限与关联字段上限配套

activation 新增 128 UTF-8 字节上限；request ID/parent 沿用 128 字节和方向/单调序号规则，method 沿用 256 字节；发送端也执行相同检查，不仅靠接收端拒绝。协议允许的 escaped activation/method 可按每输入字节最多 6 个 JSON 字节保守估计，固定错误帧在这些边界下小于 4 KiB；实际 Go 编码测试锁住此可观察保证，不锁住内部常量的实现形式。

规划期用包含 128 个 NUL activation、128 字节 ID、256 个 NUL method 的保守信封算得固定错误为 2599 字节（含 LF）；这只是推导校核，不代替 Go wire 验收。选择 4096 而非任意 128/1024 的原因是覆盖合法关联字段的最坏转义，并保留简单可解释的整数 KiB 下限。大 initialize、注册表、业务 payload 不保证装得下，仍须实际检查。

JSON Schema 的 maxLength 按字符而非 UTF-8 字节计，不能假称仅靠 maxLength 就实现字节上限。schema 标明结构限制，codec/wire 的实际字节校验和多字节 code vectors 补足差异；`frameBytes` 是整数且 schema/Go 两边都强制范围和必需性。`contract.Decode` 当前不会自动拒绝缺失非零字段，必须在初始化/确认边界显式校验，不能只加 struct tag。

### 6. 完整调用方清单与所有权

| Owner / 层 | 必须同步的位置 | 验证职责 |
|---|---|---|
| Integrator / contract | `contract/types.go`、SessionFactory 描述符及调用签名、`schema/session.schema.json`、`schema/testdata/interoperability.json` | 必需字段/范围、启动前协议选择、合法和拒绝向量、控制方法 schema 增量合并 |
| Integrator / wire | `wire/frame.go`、`wire/session.go` 及其全部测试 helper | 首帧原始长度、绑定顺序、唯一编码路径、控制/取消、race |
| Host / Runtime | `host.go`、`session.go`、`runtime/internal/process`、native/wasm、Host fakes/adapter tests | 一次配置传递、启动前失败、确认后发布、自有 ABI 不注入 |
| SDK | `sdk/client.go`、definitions/标准入口和 SDK tests | Start 前限制、注册确认、启动失败清理；不增加作者握手样板 |
| 调用方 / 验收 | `conformance/cmd/{measure,lifecycle-smoke}`、`runtime/testdata/faultplugin`、独立作者/Host examples、CLI、README/docs 命令 | 删除隐式默认，重建 Native/WASI，观察 activation 不变和副作用计数 |

后续实施以 `rg` 检索 `FrameBytes`、`DefaultLimits`、`wire.New`、`contract.Initialize`、`contract.Registration`、`SessionFactory`、`Launch` 及手写 initialize JSON 扩充当前清单；这是受变更影响调用者的验收清单，不是把方案调查推迟到实现。共享契约、wire、SDK、schema 由 Integrator 串行集成；可委派独立测试/文档写集，但不得各自添加预算字段。

文档更新 protocol、host-guide、runtimes、plugin-author、troubleshooting 和实际受影响命令；并行文档工作的 codeCommit/codePaths 需按最终验证后的实现刷新，不把规划先写成已支持。预算未进入签名输入，无须改签名算法；受当前包/config 变更影响的 fixture 摘要由其 owner 正常重算，不能借预算工作冻结旧内容。

## Risks / Trade-offs

- [小于 4 KiB 或大于 1 MiB 的旧配置失效] -> 直接拒绝并同步所有调用者，不留旧路径；该范围是当前公开选择，不承诺任意字节值。
- [SDK 读取首帧最多 1 MiB，而宿主最终只选 4 KiB] -> 引导限额仅适用于第一帧，明确披露；在 Start 前复核 N，不将引导读取描述为全程 4 KiB 内存承诺。
- [初始化期间反向调用让两阶段 min 方案出错] -> 使用提议原值确认，所有回调前绑定，真实 Start 回调覆盖这一窗口。
- [并发读取或 ready 提前置位] -> 在 readLoop 同步校验/绑定，测试连续帧、迟到确认和 immediate request，使用 race 验证。
- [新增控制方法被误当业务] -> 与基线的 publish/retire 串行集成，共享控制分类并测试清理，预算不改变其单调生命周期。
- [fatal 清理无法通过已断连接释放远端资源] -> 报告实际失败，不把清理触发当作回收成功，不另建补偿或重连协议。
- [静态 schema 与实际字节数不等价] -> 使用实际编码/读长测试和 code vectors；保留结构向量不冒称运行证据。

## Migration Plan

1. 先完成响应隔离 change，再由 Integrator 在同一未发布契约下同步 Host/SDK/Runtime/wire/schema。删除旧字段位置和默认规则，不保留别名或版本识别。
2. 重建全部独立作者包、CLI 和 smoke 产物，现存开发 fixture 按新格式重生；缺字段二进制明确不支持，不提供迁移兼容运行模式。
3. 运行有界 wire、真实 SDK、Native/WASI、schema 和独立作者验收，检查副作用/注册/清理，记录实际平台与工具版本。
4. 需要撤回时整组撤回契约和调用者，不让同一运行配置并存两种格式；无持久业务数据迁移。
5. 主 spec 同步按 build 基线、响应隔离、本 change 顺序；本次 apply 只实施与验证，不执行主 spec 同步或归档。

## Planning Review

2026-10-27 独立只读审查覆盖两份 change 的 proposal/spec/design 和相关 wire、SDK、Host、Runtime、contract 源码。确认并修订三项：Guest 确认入队与 ready 的竞态、启动前无法区分 generic wrapper/custom Adapter、fatal 后远端清理成功承诺过强。修订同时进入两份规格和后续任务，未运行实现验收，也未把静态审查视为并发或跨平台通过证据。
