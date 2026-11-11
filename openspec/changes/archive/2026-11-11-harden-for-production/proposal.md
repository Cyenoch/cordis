## Why

流、中间件链和派生契约上线后，框架还没有在两类条件下被检验过：对端发来任意内容，以及大量调用与实例启停同时发生。第一次检验就发现调用方会在提供方停止的瞬间收到不带分类的 `EOF`。生产环境里这两类条件是常态。

## What Changes

- 调用进行中提供方停止时，调用方得到带分类的错误，而不是传输层的 `EOF`。
- 会话、schema 校验、严格解码和配置文件解析各有模糊测试；会话的模糊测试扮演任意对端。
- 会话层和宿主层各有并发压力测试：调用、流与提供方启停交错进行。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-execution`: 提供方消失时调用方得到的结果；会话面对任意对端输入的行为。

## Impact

- `hosting/bridge.go`、`hosting/chain.go` 的错误归类。
- 新增测试：`wire/soak_test.go`、`hosting/soak_test.go`、`schema/fuzz_test.go`、`contract/fuzz_test.go`、`loader/fuzz_test.go`。
