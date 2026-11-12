## Why

v0.4.0（`bound-schema-expansion-and-open-error-codes`）让引用展开预算只计展开结果，不再随嵌套深度计费。独立审查发现三个问题：

- `packagehost` 给清单中每个 definition 的配置 schema 各开一份 1 MiB 预算，展开工作量随 definition 数量线性放大。实测一个 1 MiB 的清单把约 2 KB 的放大 schema 重复 457 次，`decodeManifest`，即检查不受信任归档的路径，耗时约 50 秒、累计分配约 88 GiB（Apple M5 Pro 实测）。
- `$defs` 被计两次：根文档按包含 `$defs` 的整段计费，每个条目被引用时又计一次。v0.3.0 接受的 schema 因此在 v0.4.0 被拒绝。
- 超限错误的 `Keyword` 固定写 `$ref`，没有引用的 schema 超限时也是如此，而 `docs/contracts.md` 把 `Keyword` 说成可依赖的定位信息。

另外，归档 design 中"最坏约 66 MiB 编解码、内存同阶"的估算与实测不符，需要换成实测数字。

## What Changes

- 新增 `schema.Budget`（`schema.NewBudget()`、`(*Budget).Check`）：同一份字节预算和节点上限由多个独立 schema 共用。`schema.Compile`、包级 `Check`、`Validate` 改为各用一份新的 `Budget`，行为不变。
- `packagehost.ValidateManifest` 用一份 `Budget` 检查清单中所有配置 schema。**BREAKING**（行为收紧）：每个配置 schema 单独在预算内、合计超出的清单现在被拒绝；v0.3.0 与 v0.4.0 都接受这类清单。
- 计费修正：根文档和每次类型引用按去掉 `$defs` 后的本体计一次，取重新编码（含转义）后的长度；引用计费只对 `#/$defs/` 目标进行。`$defs` 的每个字节不再随所在文档多计一次。
- 引用展开超出字节预算、深度或节点上限时，`FieldError.Keyword` 由 `$ref` 改为 `limit`；消息文字不变。
- 不新增总工作量上限：在共享预算之后实测最坏情况，并在 design 中给出数字与理由。
- 文档：`docs/contracts.md` 写明清单共享预算、`Budget` 的用途、`limit` 关键字和实测成本；`CHANGELOG.md` 未发布一节记入三项变化。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-contracts`: 「引用展开有界」扩展到包清单的配置 schema，修正 `$defs` 的计费方式，规定超限错误的关键字。

## Impact

- 代码：`schema/schema.go`（`Budget`，`Compile` 改用它）、`schema/references.go`（计费点、错误关键字）、`packagehost/format.go`（`ValidateManifest`）、`packagehost/archive.go`（删去单个配置 schema 的检查函数）。
- 测试：`schema/references_test.go`、`packagehost/format_test.go`、`schema/gocontract/gocontract_test.go`（审查 N3：提供另一个包的服务的包能读回那份缩进写出的深层契约并生成）。
- 文档：`docs/contracts.md`、`CHANGELOG.md`。
- 兼容边界：契约格式、包格式与会话协议不变。导出的 Go API 增加 `schema.Budget` 与 `schema.NewBudget`，现有函数签名不变。清单检查比此前严格（见上），由契约生成的清单仍需在打包时通过同一检查。`FieldError.Keyword` 的取值改变，按 `docs/stability.md` 属于 v0 次版本间允许的调整，记入 CHANGELOG。
- 非目标：不提高任何限制值；不改节点计数方式；不新增总工作量上限；不改编译与规范化在每层重新编码子树的做法；不处理审查中的 N2、N4；N3 只补一个测试，不改代码。
- 源码事实与框架决定的区分：清单路径各自开预算、`$defs` 重复计费、关键字固定为 `$ref`，是 `1ef6ac7` 的源码事实；共享预算的范围、计费修正方式和 `limit` 关键字，是本变更的决定。
