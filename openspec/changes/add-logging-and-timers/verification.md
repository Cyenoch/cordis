# Verification

平台：darwin/arm64，go1.27.3。改动未提交。两个包只依赖内核。

`go test -race -count=3 ./timer ./logging` 通过；`go vet -tags=integration ./...` 通过；`bun run docs:build` 通过（28 页）。时序测试全部在 `testing/synctest` 里运行。

## 场景对照

| 需求 | 场景 | 测试 |
|---|---|---|
| 延时与周期回调 | 延时触发一次 | `TestAfterFuncFiresOnce` |
| | 提前取消、在回调里取消自己 | `TestEveryStopsWhenCanceledIncludingFromItsOwnCallback` |
| | 随实例停止 | `TestTimersStopWithTheInstance` |
| | 一次性回调不留痕迹 | `TestOneShotTimersLeaveNothingBehind` |
| | 回调 panic 使实例失败 | `TestCallbackPanicFailsTheInstance` |
| 可取消的等待与节拍序列 | 等待被取消、慢消费者 | `TestSleepAndTicks` |
| 节流与防抖 | 节流、不做尾随执行的节流、停止后的调用 | `TestThrottle` |
| | 防抖、停止后的调用 | `TestDebounce` |
| 按调用方命名的日志器 | 默认名称、设置覆盖名称 | `TestLoggerNames` |
| 级别过滤与就地调整 | 按名称的级别、作用域设置的级别、运行期调级 | `TestLevels` |
| 输出目标 | 挂接的目标随实例移除、目标出错、输出目标自带级别 | `TestAddedHandlers` |
| | 不占用标准输出 | `TestDefaultOutputIsStandardError` |
| 最近记录 | 超出容量 | `TestRecentBuffer` |
| 生命周期记录 | 启动失败被记录 | `TestLifecycleRecords` |

`TestLevels` 同时断言更新 `Format` 与 `Buffer` 后，依赖日志服务的插件和日志插件自身都没有重启。

## 官方测试的归类

对照 `third_party/cordis/packages`，提交 `35fdc96`。

| 文件 | 用例 | 归类 |
|---|---|---|
| timer | timeout：basic support、dispose | 移植：`TestAfterFuncFiresOnce`、`TestEveryStopsWhenCanceledIncludingFromItsOwnCallback` |
| | timeout：promise | 移植：`TestSleepAndTicks`（`Sleep`） |
| | interval：basic support | 移植：`TestEveryStopsWhenCanceledIncludingFromItsOwnCallback` |
| | interval：async iterator 的 break、context dispose | 移植：`TestSleepAndTicks`（`Ticks`） |
| | interval：async iterator 的 manual return/throw、concurrent reads | 不适用：Go 的 `iter.Seq` 没有手动 return/throw 和并发读取 |
| | throttle：basic support、trailing mode、no trailing mode、disposed、context dispose | 移植：`TestThrottle`、`TestTimersStopWithTheInstance` |
| | debounce：basic support、disposed、context dispose | 移植：`TestDebounce` |
| logger | keeps the bounded buffer in place and chronological | 移植：`TestRecentBuffer` |
| | disposes the exporter that registered the disposer | 移植：`TestAddedHandlers` |
| | uses fiber name when called from outside any service；honours intercept name | 移植：`TestLoggerNames` |
| | honours explicit name argument | 有意差异：用 `logging.Name` 设置或 `logger.With` 表达，没有带名称参数的调用形式 |
| | uses service name when called from inside a Service method；innermost service name；[Service.init] | 不适用：依赖 Proxy 的调用方追踪；这里名称取自显式传入的作用域 |
| | logs an error whose errors array is empty；flattens an aggregate error into its members | 不适用：错误的展开由 `slog.Handler` 决定 |
| logger-console | 全部 | 不适用：格式串、着色和时间差由具体的 `slog.Handler` 承担 |

## 与任务清单的出入

- 任务 3.1：两个包各自提供可运行的 `Example` 测试（`logging` 的 `Example`、`timer` 的 `ExampleEvery` 与 `ExampleDebounce`），文档与对照表已列出。内核示例的输出由测试逐行断言，不含带时间戳的日志和真实计时，所以不使用这两个包；加载器示例里用 `settings` 设置日志级别尚未完成。
- 4.3 CI 五平台未执行：改动尚未提交和推送。

## 独立审查（任务 4.2）

由非作者 Agent 审查 `timer` 与 `logging` 的导出 API 和实现。没有阻塞项；6 个应修项全部处理，各有回归测试。

| 问题 | 处理 | 测试 |
|---|---|---|
| 定时器与停止同时就绪时，回调可能在实例 ctx 取消后进入 | 每次回调前检查 ctx 与停止信号 | `TestNoCallbackAfterTheInstanceContextIsCanceled`、`TestStopBeforeAnImmediateTimerFires` |
| 防抖在静默期届满的同一时刻收到调用时，不经静默期就执行 | 取值与“是否已静默满 d”在同一把锁下判断，未满则按剩余时间重置 | `TestDebounceCallAtExpiryStartsANewQuietPeriod` |
| `Every`、`Ticks` 对非正周期的行为未定义 | 与 `time.NewTicker` 一致，panic；文档写明 | 文档 |
| `AddHandler` 用 `==` 比较处理器，不可比较的类型会 panic，相同值会被一起移除 | 以登记项的指针为身份 | `TestUncomparableHandlerIsRemoved` |
| `Recent` 丢失 `With`、`WithGroup` 附加的属性 | 写入缓冲时合并日志器属性，分组名拼进键，值即时求值 | `TestRecentKeepsLoggerAttributes` |
| `Config.Levels` 与调用方共享同一个 map；级别优先顺序没有测试 | 复制 map | `TestScopeLevelOverridesNameLevel` |

同时补充的文档：节流窗口与静默期从回调返回后起算；输出目标不得再经本服务写日志；日志器在日志插件停止后仍可用；`Output` 是配置的一部分。

本机（darwin/arm64，Go 1.27.3）：`go vet ./timer ./logging` 无输出，`go test -race -count=3 ./timer ./logging` 通过。
