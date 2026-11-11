# Tasks

各组按顺序执行，不并行：第 2 组需要第 1 组给根包留出的位置。`verification.md` 由各任务共同追加。

## 1. Host 位于 hosting 包

写集：Host 源码与测试（位于 `hosting/`），以及仓库内对它的全部引用。

- [x] 1.1 Host 源码与测试位于 `hosting/`（包名 `hosting`），`cmd/cordis`、`examples/basic-host`、`conformance/cmd` 和 `package.json` 的 `docs:examples` 包路径指向它。验证：`go build ./... && go vet -tags=integration ./...` 通过；`go test -tags=integration -race ./...` 通过。
- [x] 1.2 文档里的 Host 导入、`hosting.Open` 等标识符和 `go test ./hosting` 命令指向 `hosting`：`README.md`、`CONTRIBUTING.md`、`docs/api.md`、`docs/host-guide.md`、`docs/composition-tutorial.md`、`docs/generated-service.md`、`docs/contracts.md`、`docs/recovery.md`、`docs/operations.md`、`docs/performance.md`、`docs/regressions.md`。验证：这些文件中没有把 Host API 写成根包标识符的用法（搜索 `cordis\.(Open|Host|Options|Recovery\w*|Service|Provide|Call)\b` 无结果），`go test`、`go doc` 命令的目标是 `hosting`；`bun run docs:examples` 与 `bun run docs:build` 通过；`bash conformance/run.sh` 在本机通过并记录输出的平台。

## 2. 内核

写集：根包新增的内核源码与测试、`examples/kernel`，以及内核的文档：`docs/kernel.md`、`docs/composition.md`、`docs/api.md`、`README.md`、`ROADMAP.md`、站点导航。每个任务开始前先读 design.md 决定 13 列出的官方测试文件，把用例按“移植 / 有意差异 / 不适用”归类写入 `verification.md`。测试放在 `cordis_test` 包，表驱动，时序用 `testing/synctest`，全部在 `-race` 下通过。某个场景依赖后面任务的能力时，在提供该能力的任务里验证，并在 `verification.md` 的场景对照表中记下测试名。

- [x] 2.1 实现 `App`、`Scope`、`Plugin[C]`、`Instance[C]`、状态机、`Load`、`Wait`、`Stop`、`Restart`、带校验的默认 `Update`、`Defer` 与提前释放、`Go`、`Sub`、作用域按激活失效、内核关闭和可选日志。验证：规格“实例树与所有权”“资源清理与受管工作”“启动失败与显式恢复”“状态与等待”“内核关闭”中不涉及服务发布与事件的场景各有通过的测试。
- [x] 2.2 实现 `Service[T]` 的 `Provide`、`Get`、`Where`、`Changed`，`Requires` 声明、`Name`、`When`、绑定快照、两阶段停止和重启顺序。验证：规格“类型化服务发布”“依赖声明与绑定快照”“条件依赖”“Provider 变更时的重启顺序”“可单独释放的子作用域”的全部场景，以及 2.1 所列需求中涉及服务的场景，各有通过的测试。
- [x] 2.3 实现 `Isolate`、`IsolateIn`、`Realm` 和 `Setting[O]`。验证：规格“服务隔离”“作用域设置”的全部场景各有通过的测试。
- [x] 2.4 实现 `Event`、`Query`、`Chain` 三种键及其登记和执行方法、登记选项、`Filter` 与 `Within`，以及回调排空和带标记链的重入检测。验证：规格“事件监听与分发”“首个结果查询”“中间件链”“分发范围过滤”“回调并发与停止排空”的全部场景，以及前面需求中涉及监听的场景，各有通过的测试。
- [x] 2.5 实现 `OnUpdate`、就地更新作为转换、各状态下的 `Update` 和 revision 收敛。验证：规格“类型化配置更新”的全部场景各有通过的测试。
- [x] 2.6 实现 `InstanceChanged` 与 `ServiceChanged`。验证：规格“生命周期事件”的全部场景各有通过的测试。
- [x] 2.7 增加编译期约束测试：负向片段覆盖监听签名、服务值类型、设置类型和配置更新类型四种错误用法，正向片段覆盖“具体类型发布到接口键”。验证：测试断言负向 `testdata` 片段构建失败且错误信息包含不匹配的类型，正向片段构建成功。
- [x] 2.8 编写 `docs/kernel.md` 教程和可运行示例 `examples/kernel`（两个插件、服务依赖、一条中间件链、一次配置更新），为根包补充包文档和 `Example` 测试。验证：`go test -run Example .` 通过；`go run ./examples/kernel` 的输出与教程一致。
- [x] 2.9 把 `docs/composition.md` 的官方对照表改为“进程内内核”与“Host/native”两列，给出官方名称与本项目名称的对应，并列出 Go 中不提供的官方能力和 design.md 决定 12 的有意差异；在 `docs/api.md` 和 `README.md` 说明两层及其入口，把内核教程加入站点导航。验证：规格 `framework-host-integration` 的“区分进程内与 native 的能力”场景可对照文档逐项核对；`bun run docs:build` 通过。
- [x] 2.10 在 `ROADMAP.md` 增加本阶段及其后的四个阶段，分别链接到 `rebase-native-on-kernel`、`add-wire-streams-and-chains`、`add-config-loader`、`add-logging-and-timers`。验证：每一行的链接指向已存在的 change 目录。

## 3. 集成验收

- [x] 3.1 核对 `verification.md` 的场景对照表：`framework-kernel` 的每个场景都对应一个已通过的测试名。验证：对照表没有空行，列出的测试在 `go test -race .` 的输出中存在。
- [x] 3.2 由非作者独立审查内核导出 API 是否符合 Go 惯例、错误处理、停止顺序与并发边界。验证：审查结论及处理结果写入 `verification.md`，没有未处理的阻塞项。
- [ ] 3.3 运行 `go vet -tags=integration ./...`、`bash conformance/run.sh`、`bun run docs:build`、`openspec validate add-typed-composition-kernel --strict`，并在 CI 的五个平台实际运行 conformance。验证：`verification.md` 记录精确提交、本机平台输出和 CI 链接；未执行的平台明确写出。

## Workflow follow-up

- 同步 `framework-kernel` 与 `framework-host-integration` 主规格后归档本 change。
- 向下游 VortexGo 说明 Host 的导入路径是 `hosting`。
- 下一个 change：`rebase-native-on-kernel`。
