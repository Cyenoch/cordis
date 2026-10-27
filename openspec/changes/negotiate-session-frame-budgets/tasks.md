# Tasks

仅规划，以下均未实施。依赖 `isolate-oversized-responses` 的响应分类与隔离；与 build 的配置、publish/retire 和标准 SDK 入口共享文件时，由 Integrator 串行合并增量。每组同步自己的测试及文档，不另建旧格式运行路径。

## 1. 当前契约、Schema 与向量

写集：`contract/`、session schema/互操作向量、`docs/protocol.md`；owner 为 Integrator。

- [ ] 1.1 为 Initialize/Registration 加必需 FrameBytes，SessionFactory 直接改为显式 Protocol/Open 描述符，Launch.SessionFactory 与预算一起传递；验证种类/构造器缺失或未知拒绝，保留当前 schema 标识和 major/minor，签名算法与输入布局不变，不添加兼容接口。
- [ ] 1.2 将 4096..1048576 整数范围及 activation/ID/parent/method 边界写入严格校验与 session schema；用真实 codec 测试缺字段、null、零、非整数、越界、未知/重复字段和多字节边界，不把 struct tag 当作必需字段校验。
- [ ] 1.3 更新所有成功的 initialize/registration fixture，并加入当前必要字段和错误确认的拒绝向量；运行 Go contract/schema 测试与 `python3 conformance/validate-schemas.py`，分别报告结构检查和实际 codec 证据。
- [ ] 1.4 同步协议文档字段、单一显式预算、首帧硬上限与原始长度复核、失败边界，检查未写成旧协议兼容、默认1MiB或新增版本；在 schema 中合并基线的 activation.publish/retire 增量。

## 2. Wire 绑定与并发正确性

写集：`wire/` 及 wire 包的导出注释；依赖组1，由 Integrator 串行修改。

- [ ] 2.1 删除 Limits.FrameBytes 和帧零值补默认，Host 从 Options.FrameBytes 接收必需值，Guest 仅保留未绑定引导状态；同步全部 wire helpers，验证缺 Host 配置失败且 Guest 未绑定不能发请求。
- [ ] 2.2 在 readLoop 的同步准入中验证首条 initialize、保存原始长度、复核 N、绑定后再启动 handler/读下一帧；测试首帧超过N但小于1MiB、空白长度、无LF超硬上限、非法首帧及重复初始化。
- [ ] 2.3 在发送/接收初始化确认的状态转换处验证预算原值和严格结构，Guest 用发送提交屏障原子提交准入状态；测试双向 registration 后立即请求、同批 initialize+大帧、屏障期间取消/关闭及迟到确认，不能依靠 handler/调用者恢复调度防竞态或阻塞终止。
- [ ] 2.4 对发送和接收执行相同关联字节规则，复用前置 change 的固定错误；验证最低预算下最坏转义、canceled/timeout/cancel、N/N+1 和无原大响应字节，运行 `go test -race ./wire`。
- [ ] 2.5 将当前生命周期控制与普通响应分类对齐，验证 Start 反向调用已用N、publish/retire不被恢复成普通错误、实际坏帧仍关闭；检查无 runtime setter、第二编码器或隐式重新握手。

## 3. Host、Runtime 和自有 Adapter

写集：`host.go`、`session.go`、Host tests、`runtime/`、`docs/host-guide.md`、`docs/runtimes.md`；依赖组1/2，不另改共享 contract。

- [ ] 3.1 将 Host 显式配置传入 Launch、Initialize 和 Runtime，依据启动前选定的一份 factory 描述符验证通用预算；移除 native/wasm 旧帧配置来源，更新 fake sessions 和直接 Runtime 调用者，验证 generic wrapper 非法预算不启动插件或建立运行记录，custom 不被误拒。
- [ ] 3.2 更新 process 的 SessionFactory 描述符/参数透传及 Host 的 Registration 确认；验证 custom 必须提供匹配的 PluginSession 且不注入 generic 帧，generic wrapper 拿到同一个N，声明/返回不符失败而非回退；错误确认不得发布，运行 Host/Runtime 对应回归。
- [ ] 3.3 同步 Host/Runtime 配置示例与公开注释；验证 builtin-only 不依赖外部预算、自有 Adapter 明确负责自身策略、没有凭错误/零值猜测协议的回退分支。

## 4. SDK、独立调用方与作者验收

写集：`sdk/`、`cmd/` 受影响调用者、`examples/`、`conformance/`、`docs/plugin-author.md`、`docs/troubleshooting.md`、实际受影响 README/CLI 命令；依赖组1至3。

- [ ] 4.1 SDK 只消费已绑定预算并原值写回 Registration，在作者 Validate/Start 及所有反向请求前完成必要校验；测试 Start 调用数、取消、巨大注册表、本地清理及断链远端 disposer 失败可观察，不把触发清理当作释放成功，失败不会发布受管后台工作。
- [ ] 4.2 同步 measure、lifecycle-smoke、faultplugin、SDK integration、Host fakes、示例及 CLI 的 Initialize/Registration/Launch/SessionFactory 使用；用 `rg` 核对设计列出的调用者关键词，编译所有 module 并执行所改命令，不能留下缺字段 fixture 或旧位置 FrameBytes。
- [ ] 4.3 在真实独立 Native 和 WASI 插件以 4KiB 与一个较大显式预算验证双向超大普通结果、单条超限、转义、健康邻居及后续调用；检查 activation/进程未变与副作用只执行一次，运行相关 integration/race 测试。
- [ ] 4.4 更新作者及排障说明：预算来源、完整帧而非payload、分页局限、初始化失败/普通调用失败、签名与资源清理边界；按命令复现并以最终代码基线维护 docs 元数据，运行 `bun run docs:test`、`bun run docs:sync` 和受影响文档示例检查。

## 5. 独立审查与整体集成

写集：本 change 及最终验证记录；不新增业务能力。

- [ ] 5.1 非作者审查全部入口/fixture同步、最坏错误尺寸、预算切换与取消竞态、自有 ABI 边界；确认无新major、feature gate、旧字段别名、零值默认、缺字段兼容或双轨读取，并记录发现及处置。
- [ ] 5.2 运行 `bash conformance/run.sh`、schema 校验、docs 校验与 `bun run spec:validate`；实际 Windows 验证使用 `conformance/run.ps1`。记录实际平台、跳过项与未验证范围，不用结构向量或交叉编译冒充真实运行通过。

## Workflow follow-up

- 用户要求归档时先同步 build 基线，再应用响应隔离和本 delta；不要因主 spec 尚不存在而另造同义 capability。
- 规划完成后停止，等待用户明确要求 apply；这些任务不能因 OpenSpec 校验成功而勾选。
