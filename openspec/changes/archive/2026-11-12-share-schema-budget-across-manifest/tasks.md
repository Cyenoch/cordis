## 1. 共享预算与计费（schema/schema.go、schema/references.go、schema/references_test.go、schema/gocontract/gocontract_test.go）

- [x] 1.1 测试：`TestDefsAreNotChargedAsPartOfTheirDocument`（`$defs` 中 10 万个 `<`，`Check` 与 `Parse` 都接受）、`TestExpansionBudgetChargesEscapedBytes`（五次引用 10 万个 `<` 被拒绝，`x` 被接受）、`TestExpansionLimitErrorNamesLimit`（不含 `$ref` 的 20 万个 `<`，`Keyword` 为 `limit`）、`TestBudgetIsSharedAcrossSchemas`
- [x] 1.2 `Budget`、`NewBudget`、`Budget.Check`；`Compile` 改为 `NewBudget().compile`，删去 `resolveSchema`
- [x] 1.3 `expandRoot` 删去 `$defs` 后按重新编码的本体计费；`resolve` 不再按整个根文档计费；`reference` 只对 `#/$defs/` 目标计费；超限错误的关键字改为 `limit`
- [x] 1.4 变异检查：`encodedSize` 返回 `len(raw)` 时 `TestExpansionBudgetChargesEscapedBytes` 失败；本体再按含 `$defs` 的原文计费时 `TestDefsAreNotChargedAsPartOfTheirDocument` 失败；本体按未转义长度计费时 `TestExpansionLimitErrorNamesLimit` 失败；错误关键字改回 `$ref` 时同一测试失败；`reference` 也对 `#/types/` 目标计费时 `TestExpansionBudgetIgnoresWhitespace` 失败；`Budget.Check` 每次新建预算时 `TestBudgetIsSharedAcrossSchemas` 失败
- [x] 1.5 审查 N3：`TestProvidesADeepServiceOfAnotherPackage`，一个包提供另一个包的服务，那个服务的请求有 600 个字段位于 12 层结构体之下，两个包都能 `Generate`；在 `144ec7a` 之前的源码上，服务所在的包能生成，提供它的包读回契约时因字节预算失败

## 2. 清单共享预算（packagehost/format.go、packagehost/archive.go、packagehost/format_test.go）

- [x] 2.1 测试 `TestManifestConfigSchemasShareExpansionBudget`：一个约 660 KB 展开的配置被接受，两个时第二个因字节预算被拒绝；改回逐个 `schema.Check` 时该测试失败
- [x] 2.2 `ValidateManifest` 用一份 `schema.Budget` 检查全部配置 schema；删去 `validateConfigSchema`

## 3. 文档（docs/contracts.md、CHANGELOG.md）

- [x] 3.1 `docs/contracts.md`：清单共享预算、`schema.NewBudget`、计费方式、`limit` 关键字、带机器标注的实测成本
- [x] 3.2 `CHANGELOG.md` 未发布一节：清单检查收紧（比 v0.3.0 严格）、`$defs` 计费修正、错误关键字

## 4. 收尾

- [x] 4.1 重新测量审查的最坏情况（Apple M5 Pro，Go 1.27.4），数字写入 design
- [x] 4.2 `openspec validate --all --strict`、`go vet ./...`、`go vet -tags=integration ./...`、`go test -race -count=1 ./...`、`bash conformance/run.sh`、staticcheck
- [x] 4.3 VortexGo 兼容：在 VortexGo `pluginapi` 的临时副本中以 `replace` 指向本工作树，`go generate ./pluginapi/...` 与 `go test -run TestContractParses ./pluginapi/` 通过
- [x] 4.4 非作者独立审查：代码无需修改；版本按次版本发布（清单检查收紧、`Keyword` 变化、新增导出 API）；补齐下面 4.5 的测试与文档后归档
- [x] 4.5 审查补充：`TestTypeDefsAreChargedAtEachReference`（类型 `$defs` 的校验访问计费）、`TestReferenceAmplificationIsRejected` 断言 `Keyword` 为 `limit`、`TestBudgetIsSharedAcrossSchemas` 断言节点上限共享、`TestExpansionBudgetChargesEscapedBytes` 增加 U+2028 一行；四个此前存活的变异（校验访问免计、深度与节点错误关键字改回 `$ref`、不计 U+2028/2029、`Budget.Check` 重置节点数）均被测试抓到；CHANGELOG 加标记与改法；design 的 Risks 写明目录提交时的重新校验
