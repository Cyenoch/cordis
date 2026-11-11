# Verification

## 结果

本机平台：darwin/arm64，Go 1.27.3。改动未提交，CI 五平台未执行。

`bash conformance/run.sh` 在本机通过：`go test -tags=integration -race ./...` 全部通过，`basic-host` 与 `lifecycle-smoke` 用真实的 native 插件进程通过（依赖清理链、更新后重新绑定、取消、Restore、失败版本恢复、动态子实例）。`conformance/validate-schemas.py` 通过（在临时 venv 里安装 `jsonschema` 后运行）。

### 实现中发现并修复的问题

| 问题 | 修复 | 测试 |
|---|---|---|
| guest 的受管工作在宿主处理完 `initialize` 之前发请求，被会话拒绝 | guest 的请求在会话就绪前等待（`wire.Endpoint.Ready`） | `TestGuestEmitsToTheHostAndCleansUpItsOwnTree` |
| 启动阶段之后发出的请求仍被挂在 `initialize` 之下，`initialize` 应答时被取消，插件随机失败 | 插件的 `Start` 一返回就结束“启动阶段” | `TestConsumerSurvivesProviderRestarts` |
| guest 应答 `shutdown` 后立即关闭，宿主对已取消请求的应答写入失败，停用报错 | guest 等宿主关闭会话 | `TestConsumerSurvivesProviderRestarts` |
| 启动失败的插件在 `shutdown` 时把同一个失败再报成清理失败，恢复策略因此判定为清理失败 | 启动失败时当场清理，`shutdown` 不重复报告 | `TestRecoveryRetriesWithinItsBudget` |
| 启动失败的插件在清理时取消订阅，请求等待会话就绪而会话要等启动结果，entry 永远停在 Starting | 启动失败后的清理仍属于启动阶段，请求挂在 `initialize` 之下 | `TestSubscriptionRaces` |
| 停止中的激活仍能登记订阅 | 桥接在清理开始后拒绝登记 | `TestSubscriptionRaces` |
| `Wait` 在 entry 等待依赖时返回，调用方以为已启动 | `Wait` 等到 Active、Failed 或 Stopped | `lifecycle-smoke`、`TestInProcessParent` |

### 场景与测试

`hosting` 包（内存运行时，真实 SDK 与协议）：

| 规格场景 | 测试 |
|---|---|
| 进程内 provider 与 native 消费者 | `TestInProcessProviderAndNativeConsumer` |
| native provider 与进程内消费者；退役时消费者清理 | `TestNativeProviderAndInProcessConsumer` |
| 健康的子 entry 随父重启 | `TestPersistentChildKeepsItsIdentityAcrossParentRestarts` |
| 失败的子 entry 不因父重启而重试 | `TestFailedChildIsNotRetriedWhenItsParentRestarts` |
| 父重启后子 entry 重新启动、父未登记、重复的父标识 | `TestInProcessParent` |
| 可选子功能、子定义声明冲突、未知子定义 | `TestDynamicChildren` |
| 同一实例的并发调用、超过在途上限的并发调用 | `TestConcurrentCallsShareTheSessionLimit` |
| 动态订阅与取消、监听顺序与显式处理、事件跨进程、Go 类型键不外泄、混合监听者结果 | `TestEventsAndQueriesCrossTheSession` |
| guest 内子插件随父清理 | `TestGuestEmitsToTheHostAndCleansUpItsOwnTree` |
| 声明与发布不一致、运行期失败 | `TestGuestKernelFailures` |
| 启动完成后撤销端点 | `TestEndpointWithdrawnAfterStart` |
| 配置类型不匹配、更新被取代 | `TestUpdateValidatesBeforeItSavesOrStops`、`TestLaterUpdateSupersedesAnEarlierOne` |
| 临时停止、恢复、停用、Restore、Remove | `TestStopResumeDisableAndRestore` |
| Replace 与在用的包 | `TestReplaceRestartsTheEntriesOfAPackage` |
| 有限恢复与手动重试 | `TestRecoveryRetriesWithinItsBudget` |

其他包：

| 规格场景 | 测试 |
|---|---|
| 进程取消；配置类型不匹配；协议 major 不符 | `sdk` 的 `TestCancelingRunCleansUpAndReturns`、`TestInitializeChecksBeforeItStartsThePlugin`、`TestShutdownAnswersAfterTheCleanups` |
| 可跨进程的键（严格解码、查询的零值应答、上行链路分流） | `contract` 的 `TestKeysDecodeStrictlyBeforeCallingTheListener`、`TestQueryStopsAtTheFirstAnswerEvenWhenItIsZero`、`TestKeysGoThroughTheUplinkWhenTheRootProvidesOne` |
| 同进程直连、版本区间不满足、同一插件两种运行方式 | `schema` 的生成结果测试（`composable_test.go.txt`）；`conformance` 的 `TestDocumentationTutorials/generated-service`；`examples/plugins/counter` 的 `TestCounter` |
| `plugintest` 的订阅端口 | `examples/plugins/counter` 的 `go test` 通过 |
| 本机控制、恢复、dev、CLI | `cmd/cordis` 的集成测试（真实进程）全部通过 |

“once 并发准入”“native 回调自取消”“取消与已准入投递竞争”“once 与取消竞争”“初始化失败（订阅不遗留）”“退役后迟到登记”由 `hosting` 的 `TestSubscriptionRaces` 覆盖。准入点是 `hosting/bridge.go` 的 `listener.admit`：取消先把订阅标为已取消，再等已准入的投递排进会话，然后才确认。

### 独立审查

由非作者 Agent 审查 `contract`、`wire`、`sdk`、`hosting`、`runtime/memory`（本 change 与 `add-wire-streams-and-chains` 一起审查）。1 个阻塞项、8 个应修项，各有复现，全部处理；回归测试在 `hosting/review_test.go` 与 `wire/stream_test.go`。

| 问题 | 处理 | 测试 |
|---|---|---|
| 被取消的事件投递让 guest 的在途计数不归零，guest 永远停不下来（阻塞项） | 准入移到投递的处理函数里，会话不调用它时不留下任何待撤销的东西 | `TestCanceledDeliveriesDoNotKeepTheGuestFromStopping` |
| entry 启动途中被停止时以 `read frame: EOF` 报错 | `initialize` 没有成功就不发 `shutdown`，只关闭会话，其结束方式不算清理失败 | `TestStoppingAnEntryWhileItStarts` |
| `Start` 里启动的 goroutine 发出的请求晚于 `initialize` 的应答到达宿主，会话被判协议错误 | 启动阶段的请求在排入会话之前，`initialize` 不应答 | `TestRequestsOfGoroutinesStartedInStart` |
| 一次链执行多次经过同一个 guest 时互相等待名额，会话卡死 | 链的各跳不等待名额：名额用尽时该跳以 not-ready 失败 | `TestChainThroughOneGuestManyTimesDoesNotLockTheSession` |
| `Disable` 等待超时后运行门没有停止，再次启用的 entry 无法临时停止 | 停用时总是提交运行门的停止 | `TestDisableThatTimesOutLeavesTheEntryRemovable` |
| 已经完整到达的流在会话结束后被报告为失败 | 会话结束时先交付已到达的数据项和结束响应 | `TestStreamThatFinishedSurvivesTheEndOfItsSession` |
| 提前停止一个流时，provider 不及时响应取消会使整个会话在宽限期后结束 | 被取消的流立即应答，不等处理函数返回；之后的数据项不再发送 | `TestStoppingAStreamDoesNotWaitForItsProducer` |
| 受管工作在启动后立即失败时原因丢失 | guest 在会话就绪后再报告失败；`initialize` 发现插件已失败时返回其原因 | `TestWorkThatFailsRightAfterStartKeepsItsReason` |
| 订阅与链的登记没有共用配额 | 两者合计不超过 `MaxSubscriptions` | `TestRegistrationsShareOneQuota` |
| 已停止的动态子实例仍占着标识 | 停止后即移除记录 | `TestStoppedChildFreesItsIdentifier` |

审查者提到的帧层问题（取消帧排队之前就释放了在途名额，在途上限很小时对端可能判定越限）也已修复：放弃一个请求时占住发送次序，直到取消帧排入会话；回归测试 `wire` 的 `TestCancelIsQueuedBeforeTheSlotIsReused`（在途上限为 1，300 轮取消后立即复用名额）。

审查者实际运行并确认无问题的部分：停止、恢复、更新、重试与状态映射的各种组合；父子 entry 上 4 秒并发管理操作的压力测试；管理操作进行中的 `Close`；阻塞的 `Start` 期间的停止；运行记录；200 个以各种方式结束的流之后名额全部归还；在途上限为 1 时的 5000 次调用与流。未运行：真实进程运行时、Windows、恢复循环与 `Replace`/`Restore` 的竞争。

### 待办

- 尚无测试的场景：帧预算不一致时不发布、`Host.Done` 与关闭取消的细节、动态子实例的管理控制细节、订阅的发送回执细节；`runtime/testdata/subscriptionplugin` 尚未覆盖订阅场景。
- 文档：`docs/protocol.md`、`contracts.md`、`host-guide.md`、`api.md`、`plugin-author.md`、`generated-service.md`（代码块已更新，正文未改）、`cli.md`、`performance.md`、`regressions.md`、`CONTRIBUTING.md` 的正文尚未与当前 API 对齐。`getting-started.md` 与 `composition-tutorial.md` 已对齐并由测试执行。
- CI 五平台（任务 6.2）。
