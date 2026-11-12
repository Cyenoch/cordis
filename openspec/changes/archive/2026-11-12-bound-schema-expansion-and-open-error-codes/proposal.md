## Why

`schema/references.go` 的引用展开预算计的不是展开结果，而是每一层的中间副本：`expand` 在每个节点入口按原文长度计费，再在每层重新编码时按输出计费，一个叶子的字节在它的每一层祖先上各计一次，原文的缩进空白也计入。预算于是随嵌套深度和文件排版增长，与真正要限制的展开结果大小脱节。

一个实际后果：VortexGo 的 `pluginapi/cordis_gen.json`（289,227 字节，198 个 schema 文档，没有 `$ref`，紧凑后约 92 KB）在 `schema.Parse` 上被计为 1,452,308 字节，超过 1 MiB，凡是声明 `//cordis:provides` 这些服务的其他包都无法 `cordis generate`。同时，生成器第一遍处理的是推导出的、紧凑且没有 `additionalProperties:false` 的 schema，能够通过；落盘的规范形式在被重新读取时才超限，即生成器写出了自己读不回的契约，而且没有任何检查发现这一点。

另一个独立的问题：`schema/session.schema.json` 中响应的 `error.code` 是封闭枚举，而 `wire` 原样转发应用返回的带码错误（VortexGo 使用 `game_running` 等），`$defs/failure.code` 也早已是任意字符串。按发布的 schema 校验的实现会拒绝 Go 运行时发出的合法响应。

## What Changes

- 引用展开按复制进结果的紧凑字节计费：每个根文档按紧凑编码计一次，每次引用访问按目标值的紧凑编码计一次，此外不再计费。总计费不小于展开结果（含 HTML 转义带来的增长），与空白和嵌套深度无关。限制值不变（1 MiB、32 层、10000 节点）。
- 预算的范围不变：`Parse` 与 `Definition` 的 `Check`、`Normalized`、`Specs` 中，一份契约的类型、方法和 definition 配置共用一份预算；`Compile` 与包级函数 `Check`、`Validate` 以单个 schema 为范围。
- 删去按原文长度的预检：它依赖空白，且已被预算覆盖。超限的错误写明这是累计预算及其上限值。
- `cordis generate`（`gocontract`）在写出前用 `schema.Parse` 重新解析将要写出的 `cordis_gen.json`；失败时报出该文件路径和原因，两个文件都不写。
- `session.schema.json` 中响应 `error.code` 放宽为任意字符串，与运行时和 `$defs/failure` 一致；`docs/protocol.md` 列出 Cordis 自己使用的错误码，并说明应用错误码原样传递。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-contracts`: 新增引用展开的预算按复制字节计一次；生成器写出的契约必须能被重新解析。
- `framework-execution`: 新增应用错误码原样传递，会话 schema 接受任意字符串错误码。

## Impact

- 代码：`schema/references.go`、`schema/definition.go`（构造 resolver）、`schema/gocontract/analyze.go`（`Package.Contract`）、`schema/session.schema.json`。
- 测试：`schema/references_test.go`、`schema/gocontract/gocontract_test.go`、`schema/testdata/interoperability.json`、`wire`。
- 文档：`docs/contracts.md`、`docs/protocol.md`、`CHANGELOG.md`。
- 兼容边界：契约格式不变；会话 schema 只放宽（多接受一类已由运行时发出的取值），属于 `docs/stability.md` 的向后兼容修改，按旧 schema 写的消息仍然有效。导出的 Go API 不变；超限错误的文字改变（仍包含 `byte limit`）。此前因重复计费被拒绝的契约现在可能被接受。
- 非目标：不提高任何限制值；不改节点上限的计数；不改 `packagehost` 按 definition 单独检查配置 schema 的路径；不为错误码增加格式约束。
