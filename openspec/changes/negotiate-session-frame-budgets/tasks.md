# Tasks

本清单记录实际实施与验收进度，未验证项保持未完成。依赖 `isolate-oversized-responses` 的响应分类与隔离；与 build 的配置、publish/retire 和标准 SDK 入口共享文件时，由 Integrator 串行合并增量。每组同步自己的测试及文档，不另建旧格式运行路径。

## 1. 当前契约、Schema 与向量

写集：`contract/`、session schema/互操作向量、`docs/protocol.md`；owner 为 Integrator。

- [x] 1.1 为 Initialize/Registration 加必需 FrameBytes，SessionFactory 直接改为显式 Protocol/Open 描述符，Launch.SessionFactory 与预算一起传递；验证种类/构造器缺失或未知拒绝，保留当前 schema 标识和 major/minor，签名算法与输入布局不变，不添加兼容接口。
- [x] 1.2 将 4096..1048576 整数范围及 activation/ID/parent/method 边界写入严格校验与 session schema；用真实 codec 测试缺字段、null、零、非整数、越界、未知/重复字段和多字节边界，不把 struct tag 当作必需字段校验。
- [x] 1.3 更新所有成功的 initialize/registration fixture，并加入当前必要字段和错误确认的拒绝向量；运行 Go contract/schema 测试与 `python3 conformance/validate-schemas.py`，分别报告结构检查和实际 codec 证据。
- [x] 1.4 同步协议文档字段、单一显式预算、首帧硬上限与原始长度复核、失败边界，检查未写成旧协议兼容、默认1MiB或新增版本；在 schema 中合并基线的 activation.publish/retire 增量。

## 2. Wire 绑定与并发正确性

写集：`wire/` 及 wire 包的导出注释；依赖组1，由 Integrator 串行修改。

- [x] 2.1 删除 Limits.FrameBytes 和帧零值补默认，Host 从 Options.FrameBytes 接收必需值，Guest 仅保留未绑定引导状态；同步全部 wire helpers，验证缺 Host 配置失败且 Guest 未绑定不能发请求。
- [x] 2.2 在 readLoop 的同步准入中验证首条 initialize、保存原始长度、复核 N、绑定后再启动 handler/读下一帧；测试首帧超过N但小于1MiB、空白长度、无LF超硬上限、非法首帧及重复初始化。
- [x] 2.3 在发送/接收初始化确认的状态转换处验证预算原值和严格结构，Guest 用发送提交屏障原子提交准入状态；测试双向 registration 后立即请求、同批 initialize+大帧、屏障期间取消/关闭及迟到确认，不能依靠 handler/调用者恢复调度防竞态或阻塞终止。
- [x] 2.4 对发送和接收执行相同关联字节规则，复用前置 change 的固定错误；验证最低预算下最坏转义、canceled/timeout/cancel、N/N+1 和无原大响应字节，运行 `go test -race ./wire`。
- [x] 2.5 将当前生命周期控制与普通响应分类对齐，验证 Start 反向调用已用N、publish/retire不被恢复成普通错误、实际坏帧仍关闭；检查无 runtime setter、第二编码器或隐式重新握手。

## 3. Host、Runtime 和自有 Adapter

写集：`host.go`、`session.go`、Host tests、`runtime/`、`docs/host-guide.md`、`docs/runtimes.md`；依赖组1/2，不另改共享 contract。

- [x] 3.1 将 Host 显式配置传入 Launch、Initialize 和 Runtime，依据启动前选定的一份 factory 描述符验证通用预算；移除 native/wasm 旧帧配置来源，更新 fake sessions 和直接 Runtime 调用者，验证 generic wrapper 非法预算不启动插件或建立运行记录，custom 不被误拒。
- [x] 3.2 更新 process 的 SessionFactory 描述符/参数透传及 Host 的 Registration 确认；验证 custom 必须提供匹配的 PluginSession 且不注入 generic 帧，generic wrapper 拿到同一个N，声明/返回不符失败而非回退；错误确认不得发布，运行 Host/Runtime 对应回归。
- [x] 3.3 同步 Host/Runtime 配置示例与公开注释；验证 builtin-only 不依赖外部预算、自有 Adapter 明确负责自身策略、没有凭错误/零值猜测协议的回退分支。

## 4. SDK、独立调用方与作者验收

写集：`sdk/`、`cmd/` 受影响调用者、`examples/`、`conformance/`、`docs/plugin-author.md`、`docs/troubleshooting.md`、实际受影响 README/CLI 命令；依赖组1至3。

- [x] 4.1 SDK 只消费已绑定预算并原值写回 Registration，在作者 Validate/Start 及所有反向请求前完成必要校验；测试 Start 调用数、取消、巨大注册表、本地清理及断链远端 disposer 失败可观察，不把触发清理当作释放成功，失败不会发布受管后台工作。
- [x] 4.2 同步 measure、lifecycle-smoke、faultplugin、SDK integration、Host fakes、示例及 CLI 的 Initialize/Registration/Launch/SessionFactory 使用；用 `rg` 核对设计列出的调用者关键词，编译所有 module 并执行所改命令，不能留下缺字段 fixture 或旧位置 FrameBytes。
- [x] 4.3 在真实独立 Native 和 WASI 插件以 4KiB 与一个较大显式预算验证双向超大普通结果、单条超限、转义、健康邻居及后续调用；检查 activation/进程未变与副作用只执行一次，运行相关 integration/race 测试。
- [ ] 4.4 更新作者及排障说明：预算来源、完整帧而非payload、分页局限、初始化失败/普通调用失败、签名与资源清理边界；按命令复现并以最终代码基线维护 docs 元数据，运行 `bun run docs:test`、`bun run docs:sync` 和受影响文档示例检查。

## 5. 独立审查与整体集成

写集：本 change 及最终验证记录；不新增业务能力。

- [x] 5.1 非作者审查全部入口/fixture同步、最坏错误尺寸、预算切换与取消竞态、自有 ABI 边界；确认无新major、feature gate、旧字段别名、零值默认、缺字段兼容或双轨读取，并记录发现及处置。
- [x] 5.2 运行 `bash conformance/run.sh`、schema 校验、docs 校验与 `bun run spec:validate`；实际 Windows 验证使用 `conformance/run.ps1`。记录实际平台、跳过项与未验证范围，不用结构向量或交叉编译冒充真实运行通过。

## Workflow follow-up

- 用户要求归档时先同步 build 基线，再应用响应隔离和本 delta；不要因主 spec 尚不存在而另造同义 capability。
- 仅在实现及对应验收完成后勾选；用户已明确要求 apply，OpenSpec 校验不代替实现证据。

## Implementation Evidence

2026-10-27，darwin/arm64，Go 1.27.3。用户已明确要求 apply；仅在实际执行对应行为后勾选，不以 schema 或交叉编译替代运行。

- 显式 Host/Launch/wire FrameBytes、必需 Initialize/Registration 原值确认、SessionFactory Protocol/Open 描述符已接入；删除 Limits.FrameBytes。SDK/Host 文件完成预算增量后交还并行生命周期 owner，保留双方增量。
- `go test -race ./wire ./sdk ./contract ./runtime/internal/process -count=1` 通过：有界首帧/原始空白长度、下一缓冲帧限制、缺预算在作者代码前拒绝、伪造确认、UTF-8 字节边界与控制消息。
- 新增无 LF 硬上限回归发现 ReadSlice 会继续等待不完整缓冲，修正为接近硬边界时逐字节读取；1 MiB+1 首帧无需 EOF 即终止。
- Runtime/SDK 全部 integration/race 通过；额外真实 Native factory 测试确认 custom 不要求虚构 generic 预算、generic 返回 custom 明确失败并关闭，不做协议回退。
- 临时环境 `/tmp/cordis-schema-budget-venv` 运行 `conformance/validate-schemas.py`：4 metaschema、70 结构向量、1 固定签名字节/SHA-256 向量通过；18 个 code vector 不由 Python 执行，相关实际 Go 测试另行运行。项目未新增 Python 依赖。
- docs:test/sync/build 与全仓 spec strict 通过；未提交代码保留 codeCommit 告警。Windows/Linux 实机未运行。
- core/SDK 合入后，Host 的启动前拒绝、无运行记录、错误确认不发布、generic/custom 声明匹配定向 `-race` 通过。负向测试显式 Disable failed entry，并且只通过 `errors.Is` 消化其预期 Start cause，不修改 core 吞错误。
- 独立代码审查发现的两项均已修复：factory 失败路径先发起关闭再 kill/reap，最后收集关闭错误；Host 统一提取直接/间接 PluginSession 映射后按显式种类检查，process generic 的 nil 映射不误拒。真实阻塞读取 Close 回归通过，未在 Windows 实机复现。
- 首轮完整 conformance 已运行但被并行 packagehost/CLI configSchema fixture 与管理恢复 null 测试阻断，整体任务保持未完成。不是预算实现的通过记录。
- 18:29–18:31 的 `bash conformance/run.sh` 退出 0：全仓 integration/race、basic-host Native/WASI 与 lifecycle-smoke Native/WASI 均通过；并行 build 的 package/config fixture、诊断及退役 cleanup 调用链由其 owner 同步后重新执行，不以定向测试代替整仓验收，也不将它视为后续并行树已验收。
- 提交屏障回归最初存在测试自身的抢锁竞态，已用既有 lifecycleContext 调度门确定取消先于提交；定向 `-race -count=100`、迟到 Registration 回归 `-race -count=30` 与全部 wire `-race -count=3` 通过。无生产逻辑的猜测性锁或延时补丁。
- 以 `rg` 核对全部 module 的预算、wire.New、Initialize/Registration/Launch/factory 调用，未留下旧 OpenSession、Limits.FrameBytes 或缺字段默认路径；全部 module 由最终 conformance 真实构建。measure 额外以 Native/WASI 各一次激活、两次 steady call 执行通过，6 个 sample 仅证明命令可用，不作为性能结论。
- `bun run docs:examples` 与最终 docs:test/sync/build 通过，24 页/88 文件；作者文档重复预算段落已去重。4.4 的实现及文档验收已完成，但源码尚未提交，最终 codeCommit 元数据刷新仍待实际代码提交后进行，不以当前旧 SHA 或虚构 SHA 冒称新实现基线。
- 最终补充非作者审查未发现新的生产缺陷或预算调用者遗漏，指出提交屏障测试先关闭管道再读会掩盖错误 Write。已改为取消提交后通过 synctest 令真实 reader/writer 先达到等待点，再在关闭前断言不可见；两条新测试 `-race -count=100 -cpu=1,4,8` 通过。另用 Go `-overlay` 临时移除 writer 的 committed 等待（不改工作区），两种取消分支均按“success bytes escaped”预期失败，证明回归能捕捉屏障失效。
- 后续全量重跑按用户通知主动中断，退出 130，日志 `/tmp/cordis-budget-conformance-interrupted.log`。该轮已输出的包测试及 basic-host 成功不等于完整 conformance 成功；最新单源示例及其他修复后的最终门禁由并行 Integrator 负责，本方 wire/预算写集冻结交接，不再重复启动全量。
- 交接后再次核对当前状态：`git show HEAD:contract/types.go` 尚不含 FrameBytes，六份预算相关文档仍引用原 0fa22bc 基线，不能将该 SHA 刷成不含实现的 HEAD。冻结 wire 的 `go test -race ./wire -count=1 -timeout=45s`、docs:test（6/6）与 docs:sync 再次通过；sync 的基线差异告警保留。4.4 仍只待实际源码提交后的元数据刷新，不擅自提交或启动重复全量。
- 整合方于 2026-10-27T11:06:51Z 至 11:08:22Z 在 darwin/arm64、Go 1.27.3 执行最新整合树的 `bash conformance/run.sh`，报告退出 0；本方已核读 `/tmp/cordis-dx-final-conformance.log`，确认全仓 integration/race、basic-host Native/WASI、lifecycle-smoke Native/WASI 及终态成功输出。该轮覆盖单源示例、cleanup 字段、配置链路和 Host 审查修复及真实 Native/WASI preflight/managed/stderr 测试，本方未重复执行。整合方另报告标准 export 的 5 schema/25 向量与结构校验的 4 metaschema/70 向量/1 签名字节哈希通过；本方未重新运行这些 schema 命令，不将结构检查替代实际执行证据。最后非作者复核尚未结束、源码尚未提交，4.4 保持未完成；实际 API 提交 SHA、文档基线刷新及复核由整合方后续补齐，若再有代码修复须重新验收。
