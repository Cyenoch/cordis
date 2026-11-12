## Why

改了 Go 接口却没有重新生成时，`cordis pack` 只核对 `cordis_gen.json` 与构建描述，二者仍然一致，于是把旧契约和旧二进制打进包里，退出码为 0。作者以为新方法已经发布，问题要到消费方调用时才暴露。

## What Changes

- `cordis pack` 先按包声明的目标分析 Go 源码，生成文件与源码不一致时失败并提示运行 `cordis generate`。
- `packagehost.Pack` 不变：仍只读生成文件，不运行 Go 分析。
- 新增 `gocontract.Check`。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-contracts`: 打包前核对生成文件与源码一致。

## Impact

- `schema/gocontract`、`cmd/cordis`、`docs/contracts.md`、`docs/cli.md`。
