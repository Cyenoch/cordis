# Tasks

各组按顺序执行。第 3 组中宿主侧与 guest 侧在同一个提交序列里完成。`verification.md` 由各任务共同追加。

## 1. 端点键

写集：`contract` 的端点键文件及其测试。

- [x] 1.1 增加 `contract.Endpoint`、`EndpointKey`、`Require`。验证：测试覆盖 minor 不在区间时消费者 Waiting、等待原因可用 `errors.As` 取出可见的服务版本，provider 更换为满足的 minor 后消费者启动。

## 2. 生成器

写集：`schema` 的生成器及其测试、生成结果的黄金文件、`cmd/cordis` 的 `generate` 与 `build`。

- [x] 2.1 生成服务接口、端点键、服务端适配、客户端、带版本区间的依赖声明、配置类型和插件构造函数。验证：`go test ./schema/...` 通过；黄金文件能编译；规格“同进程直连”的场景有通过的测试。

## 3. 协议与两侧运行时

写集：`contract`（协议常量与负载、`EventKey`、`QueryKey`）、`wire` 的负载类型、`schema/session.schema.json`、`sdk`、`hosting`，以及说明它们的文档：`docs/protocol.md`、`docs/contracts.md`、`docs/host-guide.md`、`docs/api.md`。

- [x] 3.1 定义协议负载（schema `cordis-go.session/v1`，major 1、minor 0）：`initialize`、`shutdown`、`call`、`event.subscribe`、`event.unsubscribe`、`event`、`child`，特性白名单与 session schema。验证：`wire` 的帧、预算、在途、取消、背压测试全部通过；`python3 conformance/validate-schemas.py` 通过。
- [x] 3.2 `contract.EventKey[T]` 与 `QueryKey[In, Out]`，以及 `sdk`：`sdk.Run`、`sdk.RunTransport`、`sdk.Define`、上行链路、依赖端点转发、事件与查询的远程路径、`sdk.CreateChild`、按会话的在途名额。验证：规格“guest 内核”“可跨进程的键”“native 订阅控制”“跨执行方式一致的事件行为”的场景在内存会话上各有通过的测试。
- [x] 3.3 `hosting` 的 entry 是内核实例：宿主侧桥接、派生作用域与隔离、父标识服务、动态子实例、`Host.Scope()`、`Host.ProvideParent`、`Call`/`Caller`/`Emit`、状态与等待原因的映射、图校验、运行记录。验证：`framework-composition` 的全部场景各有通过的测试；`hosting` 的管理、子实例、恢复、重试测试通过。
- [x] 3.4 恢复循环和本机控制使用内核的状态。验证：`framework-recovery` 与 `framework-control` 的测试通过；规格“失败的子 entry 不因父重启而重试”有通过的测试。
- [x] 3.5 `docs/protocol.md`、`docs/contracts.md`、`docs/host-guide.md`、`docs/api.md`，说明会话协议、可跨进程的键、根作用域与进程内父、并发回调。验证：`bun run docs:build` 通过；这些文档只使用当前 API 的标识符。

## 4. 测试辅助、示例、验收插件与作者文档

写集：`plugintest`、`examples`、`conformance`、`runtime/testdata`、`cmd/cordis` 的 `init` 模板与集成测试，以及作者文档：`docs/plugin-author.md`、`docs/generated-service.md`、`docs/getting-started.md`、`docs/cli.md`、`docs/composition-tutorial.md`、`docs/performance.md`。

- [x] 4.1 `plugintest` 在内存会话上运行真实桥接与 guest 运行时。验证：`examples/plugins/counter` 的 `go test` 通过。
- [x] 4.2 `examples/plugins/*`、`examples/basic-host`、`conformance/cmd`、`runtime/testdata` 下的测试插件、CLI 集成测试使用的插件和 `cordis init` 的模板使用内核 API。验证：`cordis init` 生成的模块在 `go build` 和 `go test` 下通过；`go test -tags=integration -race ./...` 通过；`bash conformance/run.sh` 在本机通过并记录平台。
- [x] 4.3 作者文档说明并发回调的注意事项和 native 运行与进程内运行的两处差异。验证：`bun run docs:examples` 与 `bun run docs:build` 通过；规格 `framework-host-integration` 的“区分进程内与 native 的能力”场景可对照文档逐项核对。

## 5. 单一生命周期实现

写集：`contract` 的导出面。

- [x] 5.1 仓库里只有一套生命周期实现，即根包内核；`contract` 只导出数据类型和可跨进程的键。验证：`go build ./... && go vet -tags=integration ./...` 通过；`verification.md` 的场景对照表覆盖生命周期各规则及承担它们的测试名。

## 6. 集成验收

- [x] 6.1 由非作者独立审查 `contract`、`sdk`、`hosting` 的导出 API 与协议，并复核会话停止顺序。验证：结论与处理结果写入 `verification.md`，没有未处理的阻塞项。
- [x] 6.2 运行 `go vet -tags=integration ./...`、`bash conformance/run.sh`、`bun run docs:build`、`openspec validate rebase-native-on-kernel --strict`，并在 CI 的五个平台实际运行 conformance。验证：`verification.md` 记录精确提交、本机平台输出和 CI 链接；未执行的平台明确写出。

## Workflow follow-up

- 同步 `framework-composition`、`framework-execution` 与 `framework-host-integration` 主规格后归档本 change。
- 通知下游 VortexGo：插件需要用生成器产出代码、以 `sdk.Run` 启动、处理并发回调、用该 SDK 构建并签名。
- 下一个 change：`add-wire-streams-and-chains`。
