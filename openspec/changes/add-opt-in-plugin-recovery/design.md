# Design

## Context

基线 `1d640b0`；核心在 dispose 完成及 OnDisposed 返回后才清除 activation 并设置终态，已有 Status 包含 identity、desired/applied 和 Outcome。前台 host 已每 100ms 读取状态。后续控制 change 提供 Host.Retry 和原子条件重试，避免覆盖新配置。

## Goals / Non-Goals

策略留在 Host 层，核心只负责原子生命周期动作。默认 Options 零值仍无恢复。第一版预算以 Host 生命周期和当前登记为界，不增加持久任务队列、自动健康探测、稳定时间刷新、抖动或分布式协调。

## Decisions

### 1. 公共配置和状态

Host Options 增加 `Recovery map[string]RecoveryPolicy`，以持久 entry ID 索引；policy 包含 `MaxRetries`、`InitialDelay`、`MaxDelay`。Open 拷贝并校验所有参数；只允许存在于 Store 的 native entry，允许暂时 disabled，拒绝未知 ID。之后重新 Enable 相同 ID 仍使用配置策略，删除后仅显示缺失，不自行创建。

CLI `host -recovery FILE` 用已有严格 loader；文件为 ID 到 `{maxRetries, initialDelay, maxDelay}` 的映射，延迟使用 duration 字符串。仅启动读取，不 watch、不合并环境变量。策略是宿主配置，不写进插件包或持久 Entry。

提供 `Host.RecoveryStatus()` 的独立快照，逐项包含 entry、当前 identity/revision、used/maxRetries、state、nextRetryAt。保持 Status 的生命周期含义不变；CLI 前台与在线状态附带此快照，离线历史不假装保存了计数。

### 2. 一个受管恢复循环

配置非空时 Host 启动一个恢复协调循环，有限 ticker 扫描策略对应实例；无需核心广播 API 或每实例常驻 goroutine。每项只保存当前登记 wrapper、最后失败身份、计划时间和计数，空间随策略数有界。调度依据当前 Status，但提交通过核心原子条件重试复核 wrapper/activation/revision/Failed。

自动重试需要清理完成且 CleanupCode 为空。core 内记录本次 Failed 是否来自刚完成清理的 activation，离开该失败状态即失效；调度与条件提交均使用此事实，不从 Status 中保留的历史 identity/outcome 推断。图校验 Failed 不调度。采用饱和指数计算避免 duration 溢出。只在重试被核心接受时计数；等待依赖保持 Waiting，不再提交另一次重试。循环只提交原语，不串行等待某个插件达到 Active，避免一个卡住的初始化阻塞其他实例。

延迟从首次观察到该次终态失败开始计算；额外扫描延迟可存在，绝不早于退避期限。失败→scheduled→retrying，达到 Active 后显示 idle 但保留 used；再次失败继续剩余额度。自动初始化不添加另一套超时/强杀语义，既有取消及 Host.Close 继续负责终止。

### 3. 与人工管理协调

计划持有登记、identity、desired revision 和 Host 恢复周期。提交前在 Host 操作准入及核心锁下复核；手动操作在现有准入中先校验，实际接受变更时才失效旧计划。Update 只有配置提交被接受后开启新周期；未提交的非法配置或 Replace 预检拒绝保留原计数、计划和 nextRetryAt，不先取消再重排。Retry 只有原子提交被接受才重置自动预算，手动这一次不占自动次数。

stop/disable/remove/Replace/Close 否决旧计划；resume 或重新登记只对当前登记开启新周期。控制互斥仍由现有树预约负责。自动失败重试只影响该实例的 activation；依赖消费者随已有引擎重绑定，动态 child 由父重新创建，不主动独立恢复 child。

需要在提交与计数之间保持同一 Host 操作边界，防止两轮扫描或人工 retry 双重提交。恢复日志在锁外输出，不借日志回调触发重试。

### 4. 关闭和可观察性

Close 首先将 Host 标记关闭并取消恢复循环；循环可能正在等待操作准入，必须使用可取消 context 并在准入后复核 closed。已接受的 Close 必须提交引擎退役，不能因等待恢复循环超时而跳过。只有恢复循环退出且引擎排空后才释放 Store 和结束 Done；不能在持有循环所需锁时 join。历史 Close 错误仍保留。

native 初始化失败时不对尚未完成协议初始化的会话发 retire。进程关闭后，已归属该进程失败的同一控制通道错误不重复记成清理错误；实际 shutdown 回调错误、进程树回收和文件清理错误仍保留 CleanupCode，阻止自动恢复。

记录 scheduled、attempt、exhausted、cleanup_failed、canceled 等固定事件，带 entry/原失败身份/revision/尝试数/下次时刻，不记录配置和业务输入。新的实际 activation 继续由已有生命周期日志关联，不在提交瞬间猜测 ID。

### 5. 验证

行为测试覆盖三次失败指数封顶/预算不被短暂 Active 刷新、无策略不重试、单次业务错误不重启、慢清理和清理错误、人工停止/更新/同 ID 重建抢占、关闭排空、人工恢复刷新预算及另一实例不中断。测试只对“不得早于”做时间断言，使用同步点验证竞争，避免依赖 CI 速度。

至少一个真实签名 native 故障样例验证进程退出→清理→自动新 activation→耗尽→在线人工 retry，并纳入五平台 conformance。

## Risks / Trade-offs

- 重复初始化可能产生外部副作用 → 只显式列出的实例启用，文档要求作者保证初始化可重复；不承诺补偿。
- 重开 Host 会刷新预算 → 清楚记录会话级边界；需要跨进程总预算时由宿主应用另行实现。
- core 与管理操作存在并发 → 条件重试原语和真实 race 测试保护，不依赖 ticker 执行顺序。
