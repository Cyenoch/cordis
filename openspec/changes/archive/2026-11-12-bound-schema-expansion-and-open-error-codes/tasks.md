## 1. 引用展开计费（schema/references.go、schema/definition.go、schema/references_test.go）

- [x] 1.1 先写失败测试：同一契约紧凑与缩进形式结果相同（`TestExpansionBudgetIgnoresWhitespace`）；深层嵌套中 600 KiB 的叶子被接受（`TestExpansionBudgetCountsEachByteOnce`）；十层、每层十次引用的 `$defs` 被拒绝（`TestReferenceAmplificationIsRejected`）；共享类型测试的叶子改为 16 KiB。前两项与放大测试的节点用例在未修改的代码上因字节预算失败
- [x] 1.2 `newResolver` 紧凑化共享类型；`resolve` 校验并紧凑化根文档后计一次；引用访问在复制前计目标值一次；删去其他计费点和原文长度预检；`oneOf` 重新编码不再忽略错误
- [x] 1.3 `Definition.normalized()` 改用 `newResolver`
- [x] 1.4 测量 VortexGo `pluginapi/cordis_gen.json`（289,227 字节，198 个文档）在 `Parse` 路径上的计费：改动前 1,452,308 字节（138.5%），改动后 92,368 字节（8.8%）；节点 1,808 个，不变

## 2. 生成器自检（schema/gocontract/analyze.go、schema/gocontract/generate.go 与其测试）

- [x] 2.1 先写失败测试 `TestUnreadableContractIsNotWritten`：推导契约比预算少约 6 KB、规范化后超出约 5 KB 的包，`Generate` 失败、错误含 `cordis_gen.json` 路径和字节上限、不写文件；去掉重新解析时该测试失败（错误只指向方法 schema）
- [x] 2.2 `Package.Contract()` 用 `schema.Parse` 重新解析将要写出的内容，失败时报出文件路径；`generated()` 先取契约再取源码
- [x] 2.3 作者插件复现目录（在仓库外，`replace` 指向工作树）导入 VortexGo 契约：HEAD 上 `cordis generate` 因累计字节预算失败，工作树上成功

## 3. 会话错误码（schema/session.schema.json、schema/testdata/interoperability.json、wire）

- [x] 3.1 响应 `error.code` 改为任意字符串
- [x] 3.2 互操作向量：`error-response-application-code` 被接受，`error-response-code-must-be-a-string` 被拒绝；`conformance/validate-schemas.py`（临时 venv 中的 jsonschema）通过，同一向量在 HEAD 的 schema 上失败
- [x] 3.3 `TestApplicationErrorCodeMatchesSessionSchema`：处理函数以 `game_running` 失败时，实际写出的响应帧与被接受的向量一致

## 4. 文档（docs/contracts.md、docs/protocol.md、CHANGELOG.md）

- [x] 4.1 `docs/contracts.md` 改写预算的计费方式与范围，并写明生成前读回
- [x] 4.2 `docs/protocol.md` 列出框架错误码并说明应用错误码原样传递
- [x] 4.3 `CHANGELOG.md` 未发布一节记入三项变化

## 5. 收尾

- [x] 5.1 `openspec validate bound-schema-expansion-and-open-error-codes --strict`、`go vet ./...`、`go vet -tags=integration ./...`、`go test -race -count=1 ./...`、`bash conformance/run.sh`
- [x] 5.2 非作者独立审查
