## Why

一个会话同时承载的请求数固定为 64，是写在双方代码里的常量：并发高的插件会让调用方排队，宿主却无从调整。调用的次数、耗时和结果也只有日志可看，应用接不上自己的指标与追踪。

## What Changes

- 在途上限由宿主为每个会话选择，在初始化时告知插件；默认仍是 64。**BREAKING**：`initialize` 增加必填的 `inflight`。
- 宿主增加 `Options.OnCall`：每次路由到 native entry 的调用或流结束后，把对象、耗时和结果交给应用。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-execution`: 会话的在途上限。
- `framework-observability`: 调用观测。

## Impact

- `contract`（`Initialize.Inflight`、`Launch.Inflight`、上下界）、`wire`、`sdk`、`hosting`、`runtime`。
- `schema/session.schema.json` 与互操作向量。
