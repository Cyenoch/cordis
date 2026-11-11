# Tasks

各组按顺序执行。每组开始前先读 design.md 决定 11 所列的官方测试文件，把相关用例归类写入 `verification.md`。测试表驱动，文件操作使用临时目录。

## 1. 带路径的解码错误

写集：`contract` 的严格解码、`config`。

- [x] 1.1 严格解码的未知字段、类型不符和空值错误携带出错字段的路径，`config.Document.Decode` 用它定位错误的行列。验证：`go test ./contract/... ./config/...` 通过，测试覆盖两个同名字段分别出错时定位到各自的行列。

## 2. 注册表与只读加载

写集：`loader` 的源码与测试。

- [x] 2.1 实现注册表、`Register`、接受工厂函数的 `Default`、`RegisterSetting`、解析器接口，以及条目结构的校验。验证：规格“显式注册表与名称解析”的全部场景各有通过的测试。
- [x] 2.2 实现加载器插件：读取文件、解码到新的默认值上、把插件条目和分组加载为实例树、匿名条目的运行期标识。验证：规格“配置文件到实例树”“配置解码与错误定位”的全部场景各有通过的测试；错误中的行列与测试文件一致。
- [x] 2.3 实现条目的 `requires`、`settings`、`isolate` 和 `disabled`。验证：规格“条目的依赖、设置与隔离”“禁用”中不涉及运行期编辑的场景各有通过的测试。

## 3. 运行期编辑与写回

写集：`loader`。

- [x] 3.1 实现 `Create`、`Update`、`Remove`、`Move` 及其生效规则和同一条目重新加载的合并。验证：规格“运行期编辑”的全部场景以及“禁用”中涉及更新的场景各有通过的测试。
- [x] 3.2 实现 YAML 的保留注释写回、原子替换、写入失败或取消后的重试，以及只读格式的拒绝。验证：规格“写回”中除“写回前文件已被修改”之外的场景各有通过的测试；带注释的输入与期望输出逐字节对照。

## 4. 重新读取

写集：`loader`。

- [x] 4.1 实现 `Reload`、按标识和上次读取内容的差异、损坏文件的保护、三方比较、写回前的合并和可选的轮询监视。验证：规格“重新读取与冲突”的全部场景、“写回”的“写回前文件已被修改”场景和“配置文件到实例树”的匿名条目场景各有通过的测试。

## 5. 嵌套文件

写集：`loader`。

- [x] 5.1 实现 `include`、复合标识、`initial` 和子树各自的写回。验证：规格“嵌套配置文件”的全部场景各有通过的测试。
- [x] 5.2 实现 `patches`、键的归属记录和按归属分流的写回。验证：规格“嵌套文件的补丁”的全部场景各有通过的测试。

## 6. 等待与查询

写集：`loader`。

- [x] 6.1 实现 `Wait`、`loader.Ready`、`Entries`、`EntryOf`、`Changed` 事件，以及停止时写出未写出的变更。验证：规格“就绪等待”“状态查询与关闭”的全部场景各有通过的测试。

## 7. native 包条目

写集：`hosting` 的解析器、在用集合及其测试。

- [x] 7.1 实现 `hosting.Resolver`：把已选中的包定义作为不持久化的 entry 加载到给定作用域，并计入包的在用集合。验证：规格“native 包条目”的全部场景各有通过的集成测试；宿主的持久 entry 列表在测试前后相同。

## 8. 示例与文档

写集：`examples/loader`、`docs/`、`README.md`、站点导航。

- [x] 8.1 增加可运行示例 `examples/loader`（注册表、一份带分组和隔离的配置文件、一次运行期编辑与写回）和教程 `docs/loader.md`，为 `loader` 包补充 `Example` 测试。验证：`go test -run Example ./loader/...` 通过；`go run ./examples/loader` 的输出与教程一致。
- [x] 8.2 更新 `docs/composition.md` 的对照表（loader、include、group 的对应与差异，写回保留与不保留的内容）、`docs/api.md` 和 `README.md`。验证：规格 `framework-host-integration` 的“区分两种配置来源”场景可对照文档核对；`bun run docs:build` 通过。

## 9. 集成验收

- [x] 9.1 核对 `verification.md`：`framework-loader` 的每个场景对应一个已通过的测试名，官方测试用例的归类没有遗漏。验证：对照表没有空行。
- [x] 9.2 由非作者独立审查 `loader` 的导出 API、写回与冲突规则。验证：结论与处理结果写入 `verification.md`，没有未处理的阻塞项。
- [ ] 9.3 运行 `go vet -tags=integration ./...`、`bash conformance/run.sh`、`bun run docs:build`、`openspec validate add-config-loader --strict`，并在 CI 的五个平台实际运行。验证：`verification.md` 记录精确提交、本机平台输出和 CI 链接；未执行的平台明确写出。

## Workflow follow-up

- 同步 `framework-loader` 与 `framework-host-integration` 主规格后归档本 change。
- 下一个 change：`add-logging-and-timers`。
