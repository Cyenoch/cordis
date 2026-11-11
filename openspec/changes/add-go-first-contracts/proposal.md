## Why

写一个服务方法，作者现在要手写一份 JSON Schema（`[[services.methods]]` 的 `request`、`response`），再去用生成出来、带 `json.Number` 和 `schema.Optional` 的类型。契约的形状作者已经在 Go 里想过一遍，工具却要求再用另一种语言写一遍。

## What Changes

- 作者只写 Go：给接口加 `//cordis:service`，给启动函数加 `//cordis:plugin`。`cordis generate` 从类型推导契约，生成适配代码 `cordis_gen.go` 和契约 `cordis_gen.json`。
- 请求、响应和配置就是作者自己的结构体；约束写在 `cordis` 标签里。
- 数据 schema 增加映射：`additionalProperties` 可以是一个 schema，对应 Go 的 `map[string]T`。
- 生成的代码多两个一步到位的函数：`Provide<Service>(s, impl)` 和 `Get<Service>(s)`。
- `plugin.toml` 不写 `contract`、`contracts` 时即采用上述方式。声明式契约文件保留，用于 Go 类型表达不了的形状（带标签的联合）。
- `sdk.Define` 解码配置时不再拒绝指针和 64 位整数字段：取值范围由契约校验。

## Capabilities

### New Capabilities

- `framework-contracts`: 从 Go 源码推导服务与插件契约，并生成类型安全的适配代码。

### Modified Capabilities

- `framework-cli`: `cordis generate` 接受 Go 包目录；`cordis init` 的脚手架不含手写契约。

## Impact

- 新包 `schema/gocontract`；`schema` 增加 `Codec`；`packagehost` 的作者声明加载与构建；`cmd/cordis` 的 `generate`、`init`、`dev`。
- 示例插件、文档改用 Go 写契约。
- 不增加模块依赖：分析只用标准库和 `go list`。
