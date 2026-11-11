# Verification

## 1. Host 位于 hosting 包


- 1.1：`go build ./...`、`go vet -tags=integration ./...`、`go test -tags=integration -race ./...` 通过。
- 1.2：所列文档中 `cordis\.(Open|Host|Options|Recovery\w*|Service|Provide|Call)\b` 无结果；`go test` 命令的目标是 `./hosting`。`bun run docs:examples`、`bun run docs:build`、`bash conformance/run.sh` 通过。


## 2. 内核

平台：darwin/arm64，go1.27.3。`go test -race -count=5 .` 与 `go test -tags=integration -race .` 通过。时序测试全部在 `testing/synctest` 里运行，气泡同时检查没有 goroutine 在内核关闭后残留。

### 场景对照

| 需求 | 场景 | 测试 |
|---|---|---|
| 实例树与所有权 | 父停止先清理子树 | `TestParentStopCleansSubtreeFirst` |
| | 父重启重建子实例 | `TestParentRestartRebuildsChildren` |
| | 同一插件多次加载 | `TestSamePluginLoadedTwice` |
| | 派生作用域上的登记 | `TestDerivedScopeRegistrationsBelongToInstance` |
| 类型化服务发布 | 重复发布冲突 | `TestDuplicateProviderConflicts` |
| | 同名不同类型 | `TestSameNameDifferentType` |
| | 运行期发布 | `TestProvideWhileActive` |
| 依赖声明与绑定快照 | 依赖到达后启动 | `TestStartsWhenDependencyArrives` |
| | 未声明的获取 | `TestUndeclaredGet` |
| | 祖先声明与祖先发布准入、根作用域发布的服务 | `TestAncestorAdmission` |
| | 根作用域获取 | `TestRootScopeGet` |
| 条件依赖 | 三个场景 | `TestConditionalRequirement` |
| Provider 变更时的重启顺序 | 链式下线与恢复 | `TestChainedShutdownAndRecovery` |
| | 清理期间仍可用原绑定 | `TestBindingUsableDuringCleanup` |
| | 启动期间依赖消失 | `TestDependencyLostDuringStart` |
| 可单独释放的子作用域 | 撤销单个服务 | `TestWithdrawSingleService` |
| | 在自己的回调里释放 | `TestReleaseFromOwnCallback` |
| | 条件子作用域 | `TestConditionalSubScope` |
| 服务隔离 | 两组同名服务 | `TestTwoGroupsWithTheSameService` |
| | 隔离缺失不回退 | `TestIsolationDoesNotFallBack` |
| | 共享隔离空间 | `TestSharedRealm` |
| | 隔离组独立重启 | `TestIsolatedGroupsRestartIndependently` |
| | 按名称隔离 | `TestIsolateByName` |
| 作用域设置 | 由外到内的顺序 | `TestSettingsOuterToInner` |
| | 默认设置取自最近声明者 | `TestDefaultSettingComesFromNearestDeclarer` |
| | 隔离边界只截断默认设置 | `TestIsolationCutsOnlyDefaultSettings` |
| 事件监听与分发 | 顺序分发遇错停止 | `TestEmitStopsAtFirstError` |
| | 并行分发合并错误 | `TestEmitParallelJoinsErrors` |
| | 只触发一次的并发准入 | `TestOnceUnderConcurrentEmits` |
| | 前置与取消后重新登记 | `TestPrependAndReregister` |
| | 同名不同负载类型 | `TestSameEventNameDifferentPayload` |
| 首个结果查询 | 显式处理零值结果 | `TestQueryExplicitZeroAnswer` |
| | 无人处理 | `TestQueryUnanswered` |
| 中间件链 | 修改、短路与包裹 | `TestChainModifyShortCircuitWrap` |
| | 重复调用 next | `TestChainNextOnlyOnce` |
| | 执行取消 | `TestChainCancellation` |
| | 没有中间件 | `TestChainWithoutMiddleware` |
| 分发范围过滤 | 限定到子树 | `TestFilterToSubtree` |
| | 限定到同一解析空间、隔离不影响事件 | `TestFilterToSameRealmAndIsolationDoesNotScopeEvents` |
| 回调并发与停止排空 | 消费者清理期间 provider 仍在工作 | `TestProviderWorksWhileConsumersCleanUp` |
| | 子实例清理期间父仍在工作 | `TestParentWorksWhileChildrenCleanUp` |
| | 清理不与在途回调竞争 | `TestCleanupWaitsForInflightCallback` |
| | 回调内等待自身停止 | `TestWaitingForOwnStopIsReentrant` |
| | 嵌套回调内等待上游停止 | `TestNestedCallbackWaitingForUpstreamStop` |
| | 不合作的回调保持可见 | `TestUncooperativeCallbackStaysVisible` |
| 资源清理与受管工作 | 部分清理失败 | `TestPartialCleanupFailure` |
| | 提前释放只执行一次 | `TestEarlyReleaseRunsOnce` |
| | 服务代表调用方登记资源 | `TestServiceRegistersOnCallerScope` |
| | 受管工作失败 | `TestManagedWorkFailureFailsInstance`、`TestConsumersFollowProviderFailure` |
| | 旧激活的作用域 | `TestStaleScopeAfterRestart` |
| 启动失败与显式恢复 | 失败后显式恢复 | `TestFailedRecoversOnlyExplicitly`、`TestConsumersFollowProviderFailure` |
| | 初始配置非法 | `TestInvalidInitialConfig` |
| | panic 被隔离 | `TestStartPanicIsIsolated` |
| | 调用栈只进诊断日志 | `TestLoggingOmitsConfigAndKeepsStackOutOfStatus` |
| 类型化配置更新 | 非法配置保留原实例 | `TestInvalidUpdateKeepsInstance` |
| | 就地更新不重启、更新处理失败 | `TestInPlaceUpdate` |
| | 连续更新收敛 | `TestConsecutiveUpdatesConverge` |
| | 取消更新等待 | `TestCanceledUpdateWaitStillApplies` |
| | 重启与已停止的句柄 | `TestRestartAndStoppedHandle` |
| 状态与等待 | 等待到稳定 | `TestFailedRecoversOnlyExplicitly`、`TestStartsWhenDependencyArrives` |
| | 取消等待不影响状态 | `TestWaitCancellationLeavesInstanceAlone` |
| | 日志不含配置 | `TestLoggingOmitsConfigAndKeepsStackOutOfStatus` |
| 生命周期事件 | 观察启动与失败 | `TestLifecycleEvents` |
| | 监听者中重启实例 | `TestLifecycleListenerMayRestart` |
| | 隔离空间内的服务变化 | `TestServiceChangeStaysInItsRealm` |
| 内核关闭 | 已排空但带错误、关闭后的登记 | `TestCloseReportsCleanupErrorAndCompletion` |
| | 超时未排空 | `TestCloseTimeoutCanBeResumed` |
| 类型化键的编译期约束 | 两个场景 | `TestTypedKeysAreCheckedAtCompileTime`（`integration` 标签） |

`OnUpdate` 的配置类型不符由 `TestOnUpdateConfigTypeMismatch` 覆盖。

“观察启动与失败”实际送达的转换是 Waiting→Starting、Starting→Quiescing、Quiescing→Failed：失败的启动先清理已登记的资源，再进入 Failed。

### 官方测试的归类

对照 `third_party/cordis/packages/core/tests`，提交 `35fdc96`。

| 文件 | 用例 | 归类 |
|---|---|---|
| fiber | plugin error；update recovers a failed fiber；update waits for asynchronous recovery；update surfaces a failed recovery/reload；fail on first load；keep a running plugin on update | 移植：`TestFailedRecoversOnlyExplicitly`、`TestInvalidInitialConfig`、`TestInvalidUpdateKeepsInstance`、`TestInPlaceUpdate` |
| | failed fiber does not re-enter on dependency refresh | 移植：`TestFailedInstanceIgnoresDependencyRefresh` |
| | settles a pending/failed fiber to DISPOSED on dispose | 移植：`TestStopWaitingAndFailedInstances` |
| | dispose error；keeps the active dispose sequence stable | 移植：`TestPartialCleanupFailure`、`TestParentStopCleansSubtreeFirst` |
| | restart wrapped fiber；update config while injected service reloads | 移植：`TestRestartAndStoppedHandle`、`TestWaitingInstanceLoadsOnlyLatestConfig` |
| | inertia lock 1–3 | 有意差异：转换由每实例一个驱动协程串行化，由 `TestConsecutiveUpdatesConverge` 保护收敛 |
| | update does not leak a dropped failure；update config on wrapped fiber | 不适用：未处理的 Promise 拒绝、Proxy 包装的 fiber |
| | keep the issues and their paths；pass the resolved config to listeners；keep the source config when a listener handles the update | 不适用：schema 的 issue 路径与配置解析属于 `add-config-loader`；内核的配置是已类型化的值 |
| plugin | apply functional/object plugin；nested plugins | 移植：全部测试都以 `Plugin[C]` 加载，`TestParentStopCleansSubtreeFirst` |
| | inactive context | 移植：`TestStaleScopeAfterRestart`、`TestCloseReportsCleanupErrorAndCompletion` |
| | root dispose | 有意差异：`Close` 是终态，`TestCloseReportsCleanupErrorAndCompletion` |
| | apply invalid plugin；context inspect；ctx.registry；compare snapshot；Service.init | 不适用：动态类型检查、Proxy、按插件索引的 registry、类构造 |
| dispose | dispose by plugin；dispose manually；return/yield with error | 移植：`TestEarlyReleaseRunsOnce`、`TestPartialCleanupFailure` |
| | yield dispose 及全部 async return/yield | 有意差异：组合式 effect 由 `Sub` 表达（`TestWithdrawSingleService`、`TestReleaseFromOwnCallback`）；Go 的 `Start` 是阻塞函数，没有生成器形式 |
| service | pending inject；multiple injects | 移植：`TestStartsWhenDependencyArrives`、`TestChainedShutdownAndRecovery` |
| | dispose dependents before provider（三个） | 移植：`TestChainedShutdownAndRecovery`、`TestIsolatedGroupsRestartIndependently` |
| | traceable effect（两个）；compare snapshot | 不适用：Proxy 追踪；由 `TestServiceRegistersOnCallerScope` 的显式作用域替代 |
| service-dispose | keeps provider listeners alive through the final consumer event | 移植：`TestProviderWorksWhileConsumersCleanUp` |
| | releases dependencies after a cleanup error | 移植：`TestCleanupErrorStillReleasesProvider` |
| | keeps an inherited service accessible to a child；retains an ancestor injection until its child finishes cleanup；waits for a child that explicitly injects its parent service | 移植：`TestAncestorAdmission`、`TestAncestorBindingOutlivesChildCleanup`、`TestParentWorksWhileChildrenCleanUp` |
| | withdraws one binding…；withdraws only the old binding… | 移植为 `Sub` 的形式：`TestWithdrawSingleService` |
| | retains transitive dependencies while a consumer providing another service drains | 移植：`TestChainedShutdownAndRecovery` |
| | waits for a disposing consumer that has already left the registry；replaces a binding from the same provider during consumer initialization | 有意差异：没有按插件索引的 registry；初始化中的消费者被取消（`TestDependencyLostDuringStart`） |
| | allows unrelated providers and sibling effects to clean up concurrently | 有意差异：清理按登记逆序顺序执行 |
| isolate | does not expose own provisions across isolation boundaries | 移植：`TestOwnProvisionHiddenAcrossIsolation` |
| | does not inherit the injected view across isolation boundaries；keeps own provisions separate from the injected view | 移植：`TestIsolationDoesNotFallBack`、`TestIsolationCutsOnlyDefaultSettings` |
| | isolated context；shared label | 移植：`TestTwoGroupsWithTheSameService`、`TestSharedRealm` |
| | isolated event | 移植：`TestServiceChangeStaysInItsRealm`、`TestFilterToSameRealmAndIsolationDoesNotScopeEvents` |
| intercept | merge context intercepts from outer to inner | 移植：`TestSettingsOuterToInner` |
| | take injected config from the declaring fiber；merge injected config under context intercepts | 移植：`TestDefaultSettingComesFromNearestDeclarer` |
| | stop looking for injected config at an isolation boundary | 移植：`TestIsolationCutsOnlyDefaultSettings` |
| | take no injected config for own provisions；take injected config from the def site；custom merge | 不适用：依赖 Proxy 的定义点追踪；合并方式由 provider 自己决定，`Values` 只返回有序序列 |
| events | ctx.on()；ctx.once()；ctx.emit()；ctx.parallel() | 移植：`TestPrependAndReregister`、`TestOnceUnderConcurrentEmits`、`TestEmitStopsAtFirstError`、`TestEmitParallelJoinsErrors` |
| | ctx.serial()；ctx.bail() | 有意差异：合为 `Ask`，显式 ok（`TestQueryExplicitZeroAnswer`、`TestQueryUnanswered`） |
| | ctx.waterfall() 及 rejects duplicate next()（两个）；supports nested async calls | 移植：`TestChainModifyShortCircuitWrap`、`TestChainNextOnlyOnce` |
| | ctx.waterfall() rejects continuations from outer frames | 有意差异：`next` 是普通函数值，只限制调用次数 |
| | does not accumulate across reloads；can be disposed explicitly | 移植：`TestDerivedScopeRegistrationsBelongToInstance`、`TestPrependAndReregister` |
| | global listeners are not tied to a child fiber reload | 有意差异：`Global` 只表示不受过滤，监听始终归属登记它的实例 |
| | symbol event names（三个）；prototype property names；removes empty event buckets | 不适用：JavaScript 的 symbol 与原型；空桶清理是实现细节 |
| update | coalesces updates during unloading；skips superseded loads before executing the plugin | 移植：`TestConsecutiveUpdatesConverge` |
| | waits for dependencies and loads only the latest config；does not load a queued config after disposal | 移植：`TestWaitingInstanceLoadsOnlyLatestConfig` |
| | cleans up a superseded failure without failing the latest update | 移植：`TestConsecutiveUpdatesConverge`（被取消的启动不算失败） |
| | rejects all waiting updates when the latest load fails | 有意差异：被取代的等待者得到 `ErrSuperseded` |
| | does not restart when a listener accepts an update during loading | 有意差异：启动中途的更新取消并重新启动 |
| | stops advancing an outdated async generator and collects its final yield | 不适用：生成器 |
| associate、shadow、invoke、reflect、decorator | 全部 | 不适用：Proxy、可调用服务、装饰器 |
| logger | 全部 | 属于 `add-logging-and-timers` |

### 示例与文档（2.8–2.10）

- `go test -race -run Example . ./examples/kernel` 通过；`examples/kernel` 的 `Example_run` 断言的输出与 `docs/kernel.md` 引用的输出相同。
- `docs/composition.md` 的对照表分为“进程内内核”与“Host/native”两列，并列出有意差异和 Go 中不提供的能力；`docs/api.md`、`README.md` 说明两层；站点导航加入“进程内内核”。`bun run docs:build` 通过（26 页）。
- `ROADMAP.md` 新增的五行都指向已存在的 change 目录。

## 3. 集成验收

- 3.1：上表列出的测试名都出现在 `go test -tags=integration -list '.*' .` 的输出中。
- 3.2 独立审查（非作者 Agent，只读）：对内核提出 1 个阻塞问题和 12 个应修问题，均附复现测试。处理结果：

| 编号 | 问题 | 处理 | 回归测试 |
|---|---|---|---|
| B1 | 对同一句柄 `Stop` 两次、关闭后再 `Stop` 会重复关闭通道使进程崩溃 | 已修：`Stop` 与终态处理幂等 | `TestStopIsIdempotent`、`TestSubReleaseReportsOnce` |
| S1 | 父停止期间加载的子实例成为孤儿 | 已修：在同一临界区内确认无子实例并关闭作用域 | `TestLoadRacingWithStopLeavesNoOrphan`（真实并发，300 轮） |
| S2 | 过时启动的迟到失败覆盖新 revision | 已修：过时的启动与就地更新的结果一律不计 | `TestLateFailureOfObsoleteStartIsIgnored` |
| S3 | 过滤谓词在持锁时执行 | 已修：先快照再在锁外过滤 | `TestFilterMayCallIntoTheKernel` |
| S4 | `OnUpdate` 无锁读取且接受已失效激活的作用域 | 已修：持锁并返回失效错误 | `TestOnUpdateThroughStaleScope` |
| S5 | 中间件链对不可比较的错误类型 panic | 已修 | `TestChainHandlesUncomparableErrors` |
| S6 | 生命周期事件在首个失败的监听者处中断 | 已修：逐个送达并分别记录错误 | `TestLifecycleReachesEveryListener` |
| S7 | 条件子作用域的失败和子作用域的清理错误不可见 | 已修：归到所属实例 | `TestConditionalScopeFailureFailsItsInstance`、`TestSubScopeCleanupErrorIsReportedByOwner` |
| S8 | 服务类型检查不一致，nil 接口值报错或 panic | 已修：按键的类型比较 | `TestServiceTypeChecks` |
| S9 | 发布在子作用域上的服务对本实例及后代不准入 | 已修 | `TestSubScopeProvisionIsOwnProvision` |
| S10 | 根作用域上的后台工作失败会停掉整棵树 | 已修：只写日志 | `TestRootScopeWorkFailureDoesNotStopTheKernel` |
| S11 | 在回调里调用 `Close` 不会开始关闭 | 已修：先提交再返回重入错误 | `TestCloseFromCallbackStartsShutdown` |
| S12 | 每次状态变化唤醒全部实例，关闭耗时随实例数平方增长 | 已缓解：只唤醒有事可做的实例；未重新测量耗时 | 无 |
| M1 | 从未可见的服务也会发出“不可用”事件 | 已修 | 无 |
| M2 | 后台工作在取消后返回自己的错误会使实例失败 | 已修并写入 design | `TestWorkErrorAfterCancellationIsNotAFailure` |
| M3 | `Restart` 使等待中的 `Update` 得到“已被取代” | 已修 | `TestRestartDoesNotSupersedeUpdate` |
| M4–M9 | 文档与注释不准确；全局监听者收到其他解析空间的服务变化 | 已修 | 无 |
| M10 | 重入检测偏保守：消费者的回调更新 provider 时即使可就地应用也返回重入错误 | 未改：保守方向不会死锁 | 无 |

审查还指出一个设计层面的后果：消费者的清理在 provider 重启期间失败时，它进入 Failed 且不会自动重新绑定。这符合规格“Failed 只能显式恢复”，保持不变。

`go test -race -count=10 .` 通过。

## CI 五平台

提交 `5c1aeb1`（`main`）：

- Conformance：https://github.com/Cyenoch/cordis/actions/runs/43013279520 。`go vet -tags=integration ./...`、`go mod verify` 与完整验收脚本（`conformance/run.sh`，Windows 为 `conformance/run.ps1`）在 ubuntu-24.04（x64）、ubuntu-24.04-arm、macos-15（arm64）、macos-15-intel、windows-2025 上全部通过；契约校验作业（`validate-schemas.py` 与 `spec:validate`）通过。
- Documentation：https://github.com/Cyenoch/cordis/actions/runs/43013279400 ，通过。

此前两轮（`1a42ce3`、`78c031f`）在 CI 上暴露并已修复的问题：应答发出后对端可在本端释放在途名额之前发来下一个请求，被判越限（`wire`，写出端空闲时先释放名额再交出应答）；`logging` 的测试在 Windows 上没有先关闭临时文件；两个测试依赖时序（链并发测试要求至少一次成功、流测试用固定时长等待）。
