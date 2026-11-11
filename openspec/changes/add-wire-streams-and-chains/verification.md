# Verification

本机平台：darwin/arm64，Go 1.27.3。改动未提交，CI 五平台未执行。

`bash conformance/run.sh` 在本机通过，包含 `stream-smoke`：真实的 native 插件 `examples/plugins/feed` 提供流式方法并登记跨进程中间件，宿主逐项消费 200 项、提前停止后会话仍可用，链经过插件与宿主两侧的中间件，插件停用后链不再经过它。`conformance/validate-schemas.py` 通过（临时 venv 里安装 `jsonschema` 后运行）。

## 场景与测试

| 需求 | 场景 | 测试 |
|---|---|---|
| 流式服务方法 | 逐项到达、背压、提前停止、中途失败、单项过大、同进程 | `hosting` 的 `TestStreamsAcrossTheSessionAndInProcess`（native 与进程内各一遍）；帧层 `wire` 的 `TestStreamDeliversItemsUnderBackpressure`、`TestStreamEndsEarlyOrWithAnError`、`TestOversizedItemFailsOnlyItsStream`；生成代码 `schema` 的 `TestGeneratedStreamingMethods` |
| | 消费者停止 | `hosting` 的 `TestGuestConsumesAStreamOfTheHost` |
| | 消费节奏不触发超时、被调用方静默 | `wire` 的 `TestStreamDeliversItemsUnderBackpressure`（暂停 5 分钟）、`TestStreamTimesOutOnlyWhenTheProducerIsSilent` |
| | provider 进程中途结束 | `hosting` 的 `TestStreamEndsWhenItsProviderGoesAway` |
| 跨进程中间件链 | 两个进程的中间件、中间件所属实例停止 | `hosting` 的 `TestChainAcrossProcesses` |
| | 短路、重复调用 next、迟到的 next、取消传递 | `hosting` 的 `TestChainMiddlewareMisbehaving` |
| | 中间件进程中途结束 | `hosting` 的 `TestChainMiddlewareProcessGoesAway` |
| | 长时间的执行 | `wire` 的 `TestChainRequestsHaveNoDeadlineOfTheirOwn` |
| | 与内核 `Chain` 相同的次序和选项 | `contract` 的 `TestChainKeyMatchesTheKernelChain` |
| 流与链的特性协商 | 对端不支持 | `hosting` 的 `TestPackageThatNeedsStreamsOnAHostWithoutThem` |
| | 宿主不支持中间件链 | `hosting` 的 `TestChainsNeedAHostThatCarriesThem` |
| | 未使用特性的插件 | `rebase-native-on-kernel` 的全部 `hosting` 测试与 `lifecycle-smoke` |
| 帧层的协议错误 | 未知编号或越额的数据项使会话失败、流结束后的额度帧被忽略 | `wire` 的 `TestStreamProtocolViolationsEndTheSession` |

“已取消请求上迟到的数据项被丢弃”由 `TestStreamEndsEarlyOrWithAnError` 间接覆盖（提前停止后会话继续可用），没有单独断言丢弃的帧数。

## 独立审查

与 `rebase-native-on-kernel` 一起进行，结论与处理见该 change 的 `verification.md`“独立审查”一节；其中流的两项、链的一项和配额一项属于本 change。

## 待办

- 文档（任务 1.2、2.2、5.2）：`docs/protocol.md`、`docs/generated-service.md`、`docs/contracts.md`、`docs/plugin-author.md`、`docs/composition.md` 尚未说明流与链。
- CI 五平台（任务 6.2）。
