# Design

## Context

动机见 `proposal.md`。调查基线是 `bb29fab02471f80d32d7430a0dd7dad58fff91b0`，本文件记录拟实施决策，不表示功能已经存在。

### Evidence

- `.github/workflows/conformance.yml` 已有五平台 matrix；[run 40903843295](https://github.com/Cyenoch/cordis/actions/runs/40903843295) 对该 SHA 的五个 runtime job 都实际执行并通过完整验收脚本。上一轮“其他平台只有编译”的报告只覆盖本地验证，不能继续当作仓库整体状态。
- `cmd/cordis/main.go` 只有作者工具和临时 `run`。`cmd/cordis/run.go` 的 Host 完成一次状态输出或调用就关闭，不是守护宿主。
- `packagehost/store.go:OpenWithOptions` 持有目录独占锁，打开还会执行恢复；`runs.go:recoverRuns` 把遗留 running 改成 interrupted。第二个进程读取 catalog 不能代表在线状态。
- `runtime/internal/process/process.go` 已保留退出错误、处理意外 exit 0、回收进程树并有界排空 stderr；`packagehost/runs.go` 已有最多 256 条终态历史。缺的是归属清晰的输出和结构化结果，不是完全没有崩溃处理。
- `internal/core/engine.go` 已做初始化/发布确认、取消工作、消费者先清理及固定绑定；SDK 的 `health` 只表示已发布且未退役，不代表业务健康。退役会取消在途业务，不能描述成等待所有业务成功完成的无损 drain。
- `children.go` 已允许嵌套同包动态 child，`Entry.Parent` 已支持宿主管理的持久父子树；`management.go` 只枚举 `h.entries`，而 `removeChildren` 提前从 `h.dynamic` 删除尚未排空的实例。
- `internal/core/scope.go` 只有一个服务解析空间；事件只能初始化登记。`sdk/client.go` 按事件名聚合监听者，根 Host 又压缩为一个结果，不能直接叠加全局 once/prepend 而保持 builtin/native 一致。
- `Host.Close` 可以已经排空、释放 Store，却因历史初始化/清理错误返回非 nil。内部已有 `Engine.Drained`，公共接口缺少关闭完成与错误的区分。
- `packagehost/store.go:install` 拒绝同版本不同摘要，`build.go:stageAuthor` 要求覆盖全部声明平台；`archive.go:Pack` 打包整个输入目录。dev 必须尊重这三个现有约束。

官方证据固定于只读参考 `35fdc969` 的 `core/src/context.ts`、`service.ts`、`events.ts` 和相关测试；替换身份补充参考 [bb3bf8ad 的 replace 测试](https://github.com/cordiverse/cordis/blob/bb3bf8ad7a47f61c728c42157fc74c34bb31c1f2/packages/core/tests/replace.spec.ts)。官方事件使用发射对象提供的 filter，不默认沿祖先路由；waterfall 是带 next 的调用链；原地替换保留 fiber/句柄，但仍卸载并重新初始化。不能把这几项翻译成“默认子树广播”“数组串行转换”或“保留业务内存”。

## Goals / Non-Goals

### Scope Decisions

| 建议 | 决定 | 原因和边界 |
|---|---|---|
| 包管理 CLI | 做离线 list/install/select/enable/disable/remove/uninstall/status 与前台 host | 不写 Go 也能完成首次运行及不兼容配置切换；不假装独立 CLI 可直接控制活动 Host |
| 结构化日志、启动耗时、退出原因、调用错误 | 做 | 复用 slog/现有运行历史，关联真实 activation，不建观测平台 |
| 崩溃栈 | 做显式原始诊断 | builtin 捕获栈和 native stderr 都需显式开启；SIGKILL 不保证有栈 |
| 安装/更新审计 | 仅做操作结果诊断 | 区分提交前后；不承诺持久、抗篡改、完整的安全审计 |
| 动态子实例管理 | 做查询、归属和现有句柄控制 | 不增加独立持久 child，不让旧父/旧句柄控制新实例 |
| watch → build → restart | 做单包开发循环 | 暂不管理多包依赖环境，不恢复旧内存；单一本机 target |
| 指数退避、最大重试、失败隔离 | 本次不做；策略留宿主 | 重启会重复 Start 的副作用，即使不重放业务调用也不天然安全；沿用 Failed + 显式 Update |
| Linux/Windows 验收 | 已有，扩展新场景 | 不再重建 CI，不用旧成功记录认证新增能力 |
| 启动探针、健康调度 | 复用 Start/Wait/发布确认；业务探测留宿主 | 通用协议 health 不能证明业务依赖可用，不增加轮询心跳状态机 |
| drain 与停机 | 复用现有退役，补关闭完成观测 | 无损业务排空在宿主外部接单层完成；不强杀 builtin |
| 升级窗口、蓝绿、自动回退 | 本次不做 | 现有单 provider、任意业务资源与副作用不支持通用无损切流；保留手动 Replace 回旧包 |
| Host 内 isolate | 做按 key 的子树隔离 | 两组同名服务同时共享其他服务是已有组合模型的直接缺口；不恢复全局命名 realm 系统 |
| intercept | 延期，不预留字段 | 要定义调用方追踪、跨多跳传播、合并规则和 provider 校验；尚无必须依赖它的业务场景 |
| once/prepend、动态取消 | 做 | 可由现有事件派发器统一实现；native 必须逐监听者建模 |
| 作用域事件/global | 延期，继续 Host 广播 | 服务隔离与事件过滤是不同契约；有“同名事件必须定向”的真实场景时再独立设计 |
| 跨进程 waterfall | 延期 | next continuation 穿越进程及串行业务槽，不可作为小枚举补丁；本地固定 middleware 继续用 Chain |
| 任意嵌套/持久插件树 | 保留现有静态树和同包嵌套，不新建 Loader | 缺少管理视图已纳入；跨包动态安装、移动运行中的树和配置编排没有当前需求 |
| 稳定替换句柄、原地配置更新 | 延期 | 新 activation 是必要身份边界；现有按 ID 重新取得句柄可用，不为模仿 JS 引入别名转发和配置更新 hooks |
| 官方 HMR、include/patch/表达式/配置回写 | 本次不做 | dev 重启满足当前开发反馈；不做 JS 模块缓存或双向配置编辑器 |
| 日志/计时器/文件服务、插件市场 | 不内置 | Go 标准库和显式服务可用；分发平台、版本发布策略另立需求 |
| access、WASI、自动业务补偿 | 维持删除 | 不因运维建议反向恢复应用框架 |

“延期”不是隐含任务或发布门槛，不添加空实现、占位配置和兼容分支。出现上述明确业务场景时，另建 change；当前稳定发布也不以追齐官方所有功能为条件。

## Decisions

### 1. 依赖方向和写集

沿用 `contract` → `internal/core` / `sdk` / `runtime` → 根 Host 与 `packagehost` → CLI。`config` 仍只负责严格数据解析，不获得文件监视、表达式和回写职责。新增流程编排放 `cmd/cordis/`；CLI 私有的 build/watch 状态不提升为公共框架。

公开数据和接口变更、schema、SDK 能力声明由 Integrator 一次协调；实施时按功能纵切交付，每切包含测试与文档。核心 logger 使用标准库 `log/slog`，继续使用现有 `flag`；不切换 Cobra/Viper、不添加通用事件总线或可插拔观测后端。开发监视首版使用标准库轮询文件指纹，无 fsnotify 依赖。

### 2. 诊断和终态

- 为 Host/Store 注入可选 `*slog.Logger`，nil 保持库静默；CLI 的 host/dev 配置 JSON handler。仅在生命周期转换、失败调用及管理结果处输出，不逐个记录所有成功调用或轮询状态。
- 统一属性使用 operation、stage、package/definition、digest、entry、activation、parentActivation、duration、outcome、errorCode；未出现的身份不伪造。身份来自已登记实例和会话，不来自 guest 自报字段。
- 在 runtime 实际等待/终止处生成退出摘要：请求原因、退出码/可选 signal、是否实际强制终止、协议/管道错误；保留 error chain。只加一个公共结果结构及读取路径，不靠解析错误字符串、进程名或 stderr 判断退出原因。
- 复用 RunRecord 容量和持久化，增加终态原因、启动耗时、可用退出信息和独立清理结果，只保存固定结构的分类及数值；任意错误文本只通过当次 API/日志提供，避免挤占现有 runs 文件预算。终态写入安排在实际回收和清理结果已知之后，避免当前早期 EndRun 看不到后续清理错误；不额外建立历史事件数据库。builtin 状态/日志保持可见，持久 runs 仍面向现有 external activation。
- Status 保留最近一次已结束 activation 的身份供关联；当前未激活状态仍按 State 判定，不能把 last activation 当作正在运行。interrupted 的 EndedAt 保留“恢复时刻”含义。
- stderr 回调升级为身份化记录，附 truncation；此为未发布契约直接调整，不保留两个平行入口。原始 stderr 与可选 builtin 栈默认关闭，开启后用户须对其内容负责。普通诊断不自动复制业务 payload；不做猜测式关键词脱敏。
- 日志 handler 必须及时返回、不重入 Host 管理 API，且只在内部锁外调用；不新增异步队列、日志重试、审计提交事务。第三方阻塞 handler 不纳入框架可强杀范围。

### 3. 动态子树与关闭完成

扩展现有 `ownedChild` 元数据以包含 entry 快照和 owner，Host 的 Instance/Instances/Status 通过统一查找读取持久与动态项，不再另建平行 registry。Info 增加来源和父 activation；读取返回独立快照。

动态句柄绑定实际实例对象和父激活，复用现有 Update/Wait/Stop；每次有副作用的操作验证仍是登记中的同一实例。Stop 停当前动态子树并保留可见的 Stopped，父下次重新启动可重新创建；不生成 catalog 记录。Host.Disable/Remove/Resume 对动态 ID 明确拒绝，提示使用子句柄或重启其所有者；不暗中把它提升为持久实例。

`removeChildren` 先标记退役、执行停止及移除，最后删除查询元数据，使慢清理仍可定位。并发创建继续使用已有 activation 准入，不增加第二套父代际计数器。

Host 增加一个只读关闭完成信号（例如 `Done() <-chan struct{}`）：仅在 engine 排空且 Store.Close 完成后关闭；不是“Close 被调用过”。历史失败/清理错误仍从 Close 返回。dev 可以在完成信号已关闭时报告错误并继续启动候选，未完成则停止切换并保留目录。取消只是取消等待；重复 Close 沿用既有可继续排空路径，不增设 shutdown/retry/supervise 状态机。

### 4. 离线 CLI 和前台运行

离线 CLI 直接使用 `packagehost`，不创建 Host 来“启用一下再退出”。Store 新增最小的意图编辑入口，复用 validateEntry、CheckEntryGraph 和原子 catalog mutation；不直接写 JSON 文件。offline select/uninstall 的 Enabled 从所有持久 Entries 计算，不能因当前无进程而传空数组绕过反向依赖检查。

| 命令 | 输入与结果 |
|---|---|
| list | `-store DIR`，列出 installed/selected 与 entry 意图 |
| install | `-store DIR -trust FILE ARCHIVE`，验证后安装，不选中不运行 |
| select | `-store DIR -trust FILE REF`，精确 `id@version#digest` |
| enable | `-store DIR -trust FILE [-package ID] [-definition ID] [-parent ID] [-isolate KEY] [-config/-config-file] ENTRY`，新建须指定 package；保存校验后的启用意图 |
| disable | `-store DIR ENTRY`，原子停用持久子树；不删除配置 |
| remove | `-store DIR ENTRY`，仅删除已停用且无持久子项的 entry 意图，不卸载包或删除业务数据 |
| uninstall | `-store DIR REF`，使用启用反向依赖保护；不强制删除 |
| status | `-store DIR [ENTRY]`，返回 offline=true、意图和有界最近运行记录 |
| host | `-store DIR -trust FILE`，显式 Restore、前台等待、信号 Close |
| dev | `-dev-unsigned -go-package PACKAGE [-definition ID] [-config/-config-file] [-watch DIR] DIRECTORY` |

`REF` 只解析确定引用，不添加 latest/远程下载/搜索。读取类命令、disable/remove/uninstall 无须信任文件；需要选择、校验或运行代码的命令必须有信任文件。新增信任文件沿用 `TrustPolicy` 的公钥/精确 publisher→包 ID/撤销映射，公钥使用与 keygen 一致的编码，严格拒绝未知字段；不包含私钥或持久 DevDigests。状态查询不接受绕锁模式。

对已有 entry 执行 enable，未提供的 package/definition/parent/isolate/config 保持原值，只覆盖显式输入并启用；不把 flag 默认值当作用户输入。新 entry 使用既有 definition 选择规则、空 parent/isolate 和 `{}` 配置默认值。`-parent ""` 清空父项，单独的 `-isolate ""` 清空隔离列表，配置通过显式 JSON 替换；所有结果再统一校验，不引入配置 patch 语言。

Select 仍校验该包包括 disabled 在内的全部 entry。遇到新旧 schema 不兼容，流程是停用受影响子树→从叶到根 remove 旧意图→select 新包→按父子顺序用新配置 enable。remove 复用既有 DeleteEntry 和父子图检查，拒绝 enabled 项或仍有持久子项的父项；不为绕过这一限制增加强制选择、自动迁移或配置历史仓库。

host 只恢复外部包，不能重建应用 builtin 回调。Restore 使用整个前台生命周期 context；每个实例独立记录 Waiting/Failed，不把其中一个缺依赖变成整个进程的假就绪。部分失败保留进程便于观察；不自动重试。底层 store/trust 初始化失败直接退出。输出 RestoreReport 时显式投影条目错误，不能依赖现有 `Error json:"-"` 字段。

前台运行关闭使用独立、有明确文档默认值的等待期限；这个期限是调用者的等待上限，不是所有 Go callback 都可被强杀的保证。离线命令执行期间没有插件进程，因此关闭 Store 即结束。

在线启停和查询仍用应用已持有的 Host API。为通用 CLI 引入 socket/TCP、认证、端点发现和后台 owner 会构成新的产品；本次不做。支持离线维护窗口，不宣传在线运维能力。

### 5. 开发循环

开发范围是一个本地作者包、一个入口、同包 children、单一本机 target。依赖其他包/provider 的入口仍可能 Waiting，工具输出原因，不自动下载依赖或构造完整应用。已有多目标 author 必须使用单目标开发声明；首版不自动修改或裁剪它。

使用一个串行协调循环、一个输入 generation 和当前/候选两个 Host；不并行启动多个候选：

1. 轮询显式输入目录/配置文件的内容指纹；文件编辑合并。监视包括资源文件以覆盖 go:embed，但不自动追踪模块外 replace；用户通过 `-watch` 指定。
2. 捕获本轮 configuration 的解析结果和 generation，调用既有 Build。Build 会更新生成代码、二进制和 build.json，不承诺整个源码目录事务回滚。
3. 将 ZIP 和候选 Store 放在源码目录外，执行 Pack → Inspect → 打开空候选 Host → Install → Select → 无执行的 entry 保存。候选 Host 尚未 Restore。
4. 构建前后比较输入指纹；变化则丢弃候选、处理最新输入。静态预检通过后检查 generation，再开始 Close 旧 Host。
5. 等待旧 Host 的关闭完成信号并报告 Close 错误；再次检查 generation。关闭期间有新编辑时丢弃过期候选，可以暂时没有运行实例，但不得启动旧候选冒充最新。
6. 用捕获的配置 Restore 候选，启动等待有期限；超时保留真实 Waiting/Starting 并输出原因，不宣称回滚。初始化失败保持失败会话可观察，下一次输入变化才触发重建。
7. 丢弃候选或完成切换时关闭对应 Host 并清理其独立临时目录，只有完成关闭后才删除。取消退出要停止监视、取消 go build 并回收本次创建进程；不结束用户其他进程。

原始 author version 不改写，同版本不同摘要通过不同 Store 隔开。精确摘要许可和 publisher→包 ID 映射只存在本次临时 Host；失败构建不能复用旧 ZIP 充当本轮成功。

排除工具自己生成的 Go 文件、全部声明产物、build.json、临时文件及 `.git`；不是忽略所有以 `.go` 结尾的生成源码。Pack 目前仍包含整个输入目录且执行既有路径/配额检查，本变更不顺带发明 ignore/发布内容 DSL；教程明确私钥、日志、临时目录、ZIP 和敏感配置放在作者包目录外，不能承诺只打包二进制。不适合当前 Pack 输入约束的目录须显式整理，错误直接报告。

显式配置文件不套用临时文件名称过滤，当前产物排除集合随有效声明替换，旧路径改作源码后可继续监视。启动时拒绝系统临时根位于任一目录型 watch root 内的布局，避免 Go、Pack、Inspect 的工作文件反复制造输入变化；错误提示调整临时根，不全局忽略某个可能含业务源码的目录。构建复用 native 的进程组/Job 管理（共用 `internal/processtree`），取消后回收后代并限制管道等待；Go 工作目录由每次命令拥有并清理，保留调用者选择的 GOTMPDIR 根。

不选开发版本自动递增或 Store overwrite：二者都会扩大正式包身份/签名和依赖范围的语义。不开启新进程预热再切流：初始化可能先占端口/业务锁，无法通用保证不会冲突。

### 6. 按服务隔离

增加 entry/child 的 `Isolate []string`，元素为完整 service key（含 major）。采用最近显式边界，未隔离 key 继承祖先；无祖先边界则使用根空间。每个边界是该实例登记拥有的内部 token，不向 native 暴露可选择任意 namespace 的字符串。

core 的 provider 索引改为 `(scopeToken, serviceKey)`；绑定、冲突、可见 provider、Waiting 诊断、激活依赖环和退役依赖图必须使用同一解析函数。一个实例重启时 token 的登记归属可保持，activation 身份必须更新；实例移除后重新登记产生新的 token。静态 Restore 根据父子登记重建，动态 child 继承其当前所有者。

边界声明在登记时固定。Instance.Update 仍只更新业务配置；换隔离声明使用现有 Disable/Enable 重建，不增加运行中树移动 API。不同 major 分开选择，不将整个服务名默认为隔离。

Host.Call 保持根视图；增加窄的 `Host.Caller(entryID)`（最终签名在公共契约审查中固定），返回捕获该登记解析视图的 `contract.Caller`。它属于宿主调用，不假装来自该插件、不获得 cleanup bypass；登记被移除后失效。插件 Call 仍只使用已声明并固定的绑定，不动态跨域查找。

这个模型解决“两组数据库共享日志”；不支持给兄弟组贴相同 realm label、跨树动态路由、权限隔离。Scope 的父子资源所有权和事件广播均不随它隐式改变。

### 7. 动态事件订阅

调整现有 On/typed Subscribe 的公开契约，统一返回取消句柄并接收一个简单 options 值（Once、Prepend）。不同时保留旧 On 和另一套 Subscribe registry。服务 Provide 与受管 Go 仍只在初始化注册，不能因放开 On 一并放开全部启动快照。

具体签名为 `On(ctx, name, options, handler) (Subscription, error)`，typed `contract.Subscribe` 同样显式接受 ctx/options；句柄只提供 `Cancel(ctx) error`。初始化订阅也通过同步 `event.subscribe` 请求由 Host 确认，沿用 initialize 的父请求 context，故不再在 Registration 中重复返回订阅快照。native 的订阅身份由 activation 和递增序号组成，SDK 串行提交登记；Host 只保留一个序号上界拒绝重用，不积累历史墓碑。

core 持有逐监听者身份/次序/状态。初始化登记暂存，发布确认后可见；Active 登记成功立即参与之后的发射。发射取得候选快照，但在 Host 唯一的监听者准入点再次验证订阅仍存在，并保留本次投递引用；once 在此原子消费，函数报错不重投。Prepend 放队首，多次 prepend 为后登记者先执行。取消只撤销新准入，不等待当前回调；实例退役仍通过现有 work/gate 等待已准入回调。

SDK 将每个回调保存为独立订阅，使用同一标准会话的 subscribe/unsubscribe 控制消息；投递指定单个订阅，Host 不再按事件名压缩 native 结果。控制消息在 SDK 业务槽之外处理；事件 callback 本身仍占用该槽。登记结果因传输失败无法确定时，不留一份可继续投递的无主订阅：按既有会话失败路径终止该会话并清理归属，避免另建重试事务。取消确认失败返回可观察错误。

会话必须保证取消确认排在该订阅所有已准入投递帧之后发送，且只等待这些帧进入既有发送顺序，不等待回调完成。SDK 在接收投递帧时先取得回调引用，再交给异步处理/业务槽；收到取消确认后可以移除登记表引用，已接收投递持有的引用到结束时释放。once 的本地句柄在接收唯一投递时标记已消费，登记引用随即释放，本次投递继续持有回调。这样取消和 once 都不会吞掉已准入投递，也不需要长期保存已删除回调。

监听者的最终准入位于取得实例业务槽之后，避免取消等待一份尚需当前回调让出业务槽的投递。native 准入同时捕获短暂的发送完成信号；Session 的 `CallWithReceipt` 明确报告是否进入发送序列，桥接层据此结束信号，取消只等待它。wire 增加可选的接收准备回调，SDK 在读循环推进到下一帧前捕获事件处理函数；业务仍异步执行，不在读循环调用插件代码。两处机制服务于已有会话顺序，不新增异步业务调用 API 或消息队列。

once 已在 Host 消费、但投递因帧预算或取消而未发送时，Host 用同一个 `event.unsubscribe` 控制方法通知 SDK 释放登记引用；不重新启用 once 或重放输入。该清理使用既有 grace 期限，无法确认时结束失败激活，防止双方状态分叉。登记或取消的结果因已发送请求失败而不确定时，SDK 同样终止会话清理归属；明确未发送或明确拒绝的操作保留会话。

已确认取消或已消费的本地句柄再次取消返回成功。Host 对当前 activation 下格式合法但已不活动的订阅取消返回 inactive，SDK 将自己持有句柄的 inactive 视为成功；不要求 Host 保存永久墓碑证明它曾登记过。缺失/格式非法/跨激活身份仍拒绝，未知订阅的事件投递仍是错误，不能把幂等取消规则扩展成伪造投递成功。

每 activation 活动订阅首版固定上限 256（属于跨进程资源边界，非覆盖率指标），初始化和运行时共用；取消/once 消费释放活动名额，在途引用仍受既有 inflight 约束并在投递结束/会话失败时释放。复用 frameBytes、inflight、activation 校验；不引入历史消息重放、可靠队列和事件磁盘持久化。

仍只有 Sequential、Parallel、First，First 的 Handled 独立于 Value。保持当前所有回调错误可见和环检测；事件默认全 Host 广播。过滤式作用域事件/Global、fire-and-forget 和跨进程 next 没有加入本轮协议。

### 8. 恢复、升级与测试

保留显式 Update 恢复 Failed，不新增自动 RestartPolicy、指数退避或 circuit breaker。需要自动恢复的应用自行决定哪些 Start 可重复、什么时候停止尝试及如何通知运维；外部进程管理器只能解决 Host 进程退出，不能冒充单插件恢复方案。文档说明：同 Host 重复 Restore 不重试 Failed；重开 Host 后按 enabled 意图会重新运行，不能误称为跨重启失败隔离。

保留 Install → Replace：预检失败旧实例继续；开始退役后可能有停机，初始化失败不会自动回旧包。手动回退也重新校验当前信任和配置；旧包与新配置不兼容时直接失败，不增加配置历史仓库。现有 cleanup cancellation 与消费者先清理继续验收，不增加“已经有 drain”之外的另一个资源释放流程。

使用已有五平台 workflow；功能测试先聚焦，再各平台真实运行完整 conformance。新增日志/开发循环场景断言可观察行为，不用睡眠碰运气、源码字符串断言或大规模 mock 重述实现；平台无 signal 时断言空字段。只给实际运行证据打勾。

## Risks / Trade-offs

- 离线 CLI 无法查看运行中目录 → 明确拒绝锁冲突；实时查询留给宿主 API/日志，不绕锁恢复目录。
- 新日志出口同步 handler 可能拖慢用户程序 → 在锁外调用、文档约束及时返回；不引入为不合作用户代码兜底的无限队列。
- 同一事件多个 native 回调从聚合改逐个交付会增加 IPC → 为顺序/取消一致性支付必要成本，保留帧和在途边界，不扩充并行业务槽。
- dev 的候选构建期间旧代码继续，但关闭后可能没有可运行版本 → 输出真实切换阶段；不承诺零停机或自动回滚。
- 作用域影响多个依赖路径 → 共用解析路径，覆盖缺 provider、不兼容、重复、重绑、清理旧绑定和 Restore，防止只改查找不改环检测。
- 已排空与清理成功并不相同 → 完成信号与错误各自表达，历史失败不得让 dev 永久卡死，也不能吞掉仍未完成的关闭。
- 功能范围跨层但阶段互相可独立评审 → 按 tasks 分三批实现，不同时堆所有公共 API；没有验收的阶段不宣布完成。

## Migration Plan

1. 先评审本 change 的行为与公共契约草案；本轮只创建这些规划文件，不改主规格与实现。
2. 第一批完成归属诊断、动态 child 管理、关闭完成观测及对应文档。
3. 第二批完成离线意图/CLI、前台 host 和单包 dev，端到端验收后再继续。
4. 第三批完成服务隔离与动态事件，先提交契约/schema，再修改 core、SDK/Host 桥和生成模板，builtin/native 一起验收。
5. 完成独立审查、五平台执行、可执行文档和 Pages 构建后，才勾选实施任务并同步主规格/归档。当前网站不提前宣传提案功能。

项目未发布，数据格式和 API 直接同步调整；开发用旧目录需要由维护者自行重建，不增加迁移层。代码回退依赖同一提交配套工具/格式；业务数据不由本框架迁移或回滚。

## Review and Validation

2026-10-30，独立 Agent 对规划和相关基线实现完成只读审查，提出三项实施前契约缺口及一项字段语义澄清。主 Agent 已据此修改：

- 取消确认、已准入 native 投递及 once 共享明确的引用生命周期；幂等取消不依赖永久墓碑，新增竞争场景。
- 持久历史仅存固定结构结果，具体错误留在当次 API/日志；新增大错误反复发生后仍可重开的场景。
- 增加 remove 已停用叶 entry 的最小出口，覆盖不兼容配置 schema 的完整版本切换。
- enable 对已有 entry 保留未提供字段，清空必须显式输入。

修订后 `openspec validate --all --strict` 通过全部 5 项；change 包含 6 份能力规格、25 条 requirement、64 个 scenario 和 31 项未完成的实施任务。路径及 Markdown 空白检查通过。仅新增本 change 的规划文件，未修改实现或重跑 Go/native 验收；这里记录的是规划审查和格式验证，公共契约与实现仍按 tasks 接受独立审查。
