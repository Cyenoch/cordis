# 验证记录

## 场景与测试

| 场景 | 测试 |
|---|---|
| 请求与响应的 schema 来自结构体 | `schema/gocontract` `TestDerivedContract` |
| 无法表达的类型 | `TestRejectedSources`（每例断言错误带源码位置、不留下生成物） |
| 没有请求或结果的方法 | `TestClientAndEndpoint` |
| 映射的值不满足 schema | `TestEndpointRejectsWhatTheContractDoesNotAllow`；`schema` `TestMaps`、`TestGeneratedMaps` |
| 需要导入包里的服务 | `TestDerivedContract`、`TestStaleBindingsOfAnImportedService` |
| 同一个插件在进程内与 native 包中运行 | `TestPluginRunsInProcess`；`examples/plugins/counter` `TestCounter`；`conformance/run.sh` 的 native 段 |
| 超出安全整数范围的值 | `TestConstraintsHoldOnBothPaths` |
| 源码改动后未重建就打包 | `cmd/cordis` `TestCLIProcesses`（init、build、pack）；`packagehost` `TestLoadAuthorDerivedContract` |
| 新建的插件 | `cmd/cordis` `TestScaffoldBindingsAreCurrent`、`TestCLIProcesses`；`examples/pluginmeta` `TestGettingStarted` |
| 开发循环 | `cmd/cordis` `TestDevNativeEditingAndFailureRecovery`（夹具用 Go 写契约，首次启动时源码不可编译） |

已提交的生成物与生成器一致：`TestGeneratedFilesAreCurrent`、`TestScaffoldBindingsAreCurrent`、`examples/pluginmeta` `TestGeneratedDeclarationsMatchAuthor`。

推导出的契约符合公开的契约元 schema：`schema` `TestExportIndependentValidator`（Python jsonschema 4.26，校验 `gocontract/testdata/echo/cordis_gen.json` 与映射向量）。

## 独立审查

非作者审查一轮：无阻断，4 项应修，7 项次要。

| 意见 | 处理 |
|---|---|
| 类型或包与生成代码的参数同名时生成物不能编译 | 生成的参数名避开包内声明，导入别名避开参数名；`testdata/shadow` |
| 导入包的生成物过期时，清单与二进制不一致 | 生成时比对该包的 `cordis_gen.json`；`TestStaleBindingsOfAnImportedService` |
| JSON 名含引号、可空字段带 `enum`、自带编码的字节元素、嵌入带编码类型的匿名结构体、`json.Number("")` | 分别改为拒绝、枚举含 null、拒绝、拒绝、拒绝；`TestRejectedSources`、`TestAwkwardDeclarations` |
| 整数值的大 `float64` 被拒 | schema 语言既有规则，写入文档 |
| 契约按宿主平台而非构建目标推导 | 分析接受目标平台，构建传入；`TestContractOfTheBuildTarget` |
| 约束标签取值错误时没有位置 | 在字段处校验；错误类型 `gocontract.Error` 带位置 |
| 容忍类型错误会放过写错的声明 | 只容忍函数体与变量初始化里的错误 |
| 方法名与生成名冲突 | 私有生成名改为带前缀与分隔的形式；`testdata/shadow` |
| 同名嵌入字段被拒 | 外层字段遮住提升字段；同层提升冲突报错 |
| `// cordis:` 带空格被忽略 | 报错 |
| 同进程路径传递原值 | 去掉同进程捷径：一律经 JSON，行为与跨进程一致 |
| 两个生成文件非成对原子写入 | 固定顺序写入；打包时的指纹校验兜底，未做成对原子 |

## 实际运行

- macOS 15 arm64，Go 1.27.3：`bash conformance/run.sh` 通过（含 `-race` 全量集成测试与三段 native 验收）。
- 五平台 CI：见提交后的运行记录。
