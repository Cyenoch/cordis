## Context

源码事实（`1815a3c`，v0.6.0）：

- stderr 由 `runtime/internal/process` 中一个 goroutine 读取：每次读到数据就调用 `launch.Stderr`，超出 `StderrBytes` 时送一条 `Truncated` 记录，之后只排空不转发；回调返回错误后也只排空。读到 EOF 或管道被关闭时它把结果送进 `stderrDone` 并退出。进程拆除时先回收进程树，再等 `stderrDone` 至多一个 grace；超时则关闭读端，再等一个 grace，仍未结束就记录 "stderr reader did not finish within grace" 并继续拆除，此时这个 goroutine 可能还在回调里或读调用里，之后仍可能送出记录。`hosting.Options.Stderr` 原样作为 `launch.Stderr`。Host 在激活的清理链（`endRun`）里写 "activation stopped/canceled/failed" 日志，它排在会话关闭之后，所以正常情况下晚于最后一条 stderr 记录，排空超时时则不一定。
- `Host.Resume`：`instance.open()` 重新加载运行门（异步启动），然后若内核实例为 Failed 就调用 `instance.instance.Restart(ctx)`，否则 `instance.Wait(ctx)`。内核 `Restart` 清掉 `n.err`、驱动一次后等到"没有驱动在进行"。运行门此时多半尚未提供 `runKey`，驱动把实例置为 Waiting 后结束，`Restart` 返回 nil；随后运行门就绪、实例启动，启动失败不再有人报告。`hostInstance.Wait` 先等运行门、再等内核实例，并在状态为 Starting/Waiting 时继续等，`Retry` 与恢复未失败 entry 的路径都用它。
- 恢复：`initRecovery` 在 `Open` 时校验策略（正数次数与延迟、`MaxDelay >= InitialDelay`、entry 已保存、选中包为 native），`startRecovery` 仅在有策略时启动协调循环（每 25ms 在操作锁内运行 `recoverEntries`）。`recoverEntries` 在操作锁内遍历 `h.recovery`，按 entry 状态推进；`RecoveryStatus`、`Close`、`cancelRecoveryTree`、`resetRecoveryLocked` 在 `h.mu` 内读写其中的项。`Remove` 删除记录后策略仍在，协调循环把它报为 `missing`；同 ID 重新启用时策略又生效。
- 信任：`packagehost.Store.Snapshot` 在每次激活前按当前信任策略复核包，拒绝时返回 `denied`（`TrustPolicy.verify` 的各种拒绝与完整性不符）。插件自己报告的失败同样可以带 `denied`，经 `run.reported` 进入 `Outcome.FailureCode`；两者只看错误码无法区分。代码中没有"重试也不会成功"的错误分类（没有 permanent/retryable 一类的标记）。

## Goals / Non-Goals

**Goals:**

- 宿主能按激活确切知道 stderr 不会再有记录，并能据此释放每次激活的状态。
- `Resume` 的完成与错误报告与 `Enable` 后的 `Wait`、`Retry` 一致。
- 运行中创建的 entry 能得到与 `Open` 时相同语义的恢复策略。
- 恢复状态说明最近一次失败；Host 自己的信任拒绝不被重试。

**Non-Goals:**

- 不改会话协议、包格式与契约 schema。
- 不持久化恢复策略，不在 `UpdateTrust` 后自动重试。
- 不给插件报告的错误码赋予"不再重试"的含义。
- 不新增本机控制的策略命令。

## Decisions

### stderr 以一条 `End` 记录收尾

`contract.StderrRecord` 增加 `End bool`。读取 goroutine 在读循环结束（EOF 或读端被关闭）后，若回调此前没有返回错误，再调用一次回调，记录为 `{Owner, End: true}`，然后才送出 `stderrDone`。

由此得到的保证：

- `End` 与数据记录出自同一个 goroutine，按顺序调用：它是该激活的最后一条记录，之后不可能再有记录。这比"日志在排空之后"更强：排空超时后迟到的数据也排在 `End` 前面。
- 正常情况下 `End` 先于会话 `Done`、先于 Host 的 "activation …" 日志送达；拆除只在等到 `stderrDone` 后继续。
- 截断：`Truncated` 记录之后不再有数据，`End` 照常在流结束时送达（插件可能在截断后继续运行很久）。宿主可以在 `Truncated` 时就丢弃未写完的行，在 `End` 时释放该激活的状态。
- 排空超时：拆除关闭读端后不再等待。回调卡住时，`End` 在回调返回后送达，晚于会话 `Done`；回调一直不返回则不送达。读调用卡住（逃出进程树的写端仍开着、且平台的关闭不打断读）时同理。
- 回调返回错误：转发已停止（现有语义），不再调用回调，包括 `End`；宿主在返回错误时自行清理该激活的状态。

拒绝的备选：

- 另设 `Options.StderrEnd func(contract.Identity)`：要穿过 `contract.Launch` 与运行时再加一个回调，且为保证"之后没有记录"仍须由同一个 goroutine 调用；两个回调各管一半是并行的第二套接口。
- 以会话 `Done` 或 "activation stopped" 日志为结束信号：排空超时后仍可能有迟到记录，正是要消除的情形。
- 回调失败后仍送 `End`：违背"回调失败即停止转发"，并让同一个错误可能被报告两次；宿主返回错误时本就知道转发对它已结束。

`cordis` CLI 的 `forwardStderr` 按激活记录"行未结束"，此前流在行中结束时这个标记一直留着，下一段输出也会接在半行后面。现在在 `End` 处补上换行并删除标记。

### `Resume` 提交重启后与其他路径一样等待

已失败时，在持有子树预约期间以已取消的 ctx 提交 `Restart`（与 `retry` 相同，提交但不等待），释放预约和操作锁后统一 `return instance.Wait(ctx)`。不经 `retry`，因为它会增加 `Desired`：恢复不是新 revision，此前也不增加。`hostInstance.Wait` 先等运行门再等实例，Starting/Waiting 时继续等，因此只在实例 Active、Failed、Stopped 或期限到达时返回，失败时返回启动错误。

拒绝的备选：在 `Restart` 之前等运行门就绪——仍要再等一次实例；统一用 `Wait` 更少。

### 运行中登记、修改、清除恢复策略

```go
func (h *Host) SetRecovery(ctx context.Context, id string, policy RecoveryPolicy) error
func (h *Host) ClearRecovery(ctx context.Context, id string) error
```

- `SetRecovery` 取操作锁（`recoverEntries` 在操作锁内遍历策略表，写入必须与它互斥），读已保存的 entry，按与 `Open` 相同的规则校验（抽出 `checkRecovery` 供两处共用），然后在 `h.mu` 内写入并开始新周期（`Used` 归零、状态 idle、清除 `Error`）。修改即覆盖并开始新周期：新策略的预算从零算起，与"重新登记开始新周期"一致。协调循环未运行时启动它。
- `ClearRecovery` 取操作锁并删除策略；没有策略时什么也不做，返回 nil，便于宿主按自己的配置同步。
- `Remove` 在每个 entry 的持久记录确已删除（成功或 `CommitError`）后删除它的策略。`Open` 时与运行中登记的策略一视同仁：策略属于那条持久 entry，entry 删除即结束；同 ID 重新启用的是新 entry，需要重新登记。
- `Remove` 是删除持久记录的唯一途径（目录由当前 Host 独占），清除后 `recoverEntries` 不会再遇到"策略在、记录不在"，`RecoveryMissing` 不再可能出现，因此删除这个常量；`recoverEntries` 中找不到记录的情形并入"未启用"分支（零值 entry 未启用），不保留专门分支。
- 协调循环在策略表为空时直接返回，不再每 25ms 读一次目录。

拒绝的备选：

- 在 `Enable` 上增加恢复参数：`Enable` 的参数是持久的 `contract.Entry`，策略不持久；也无法修改或清除已有 entry 的策略。
- `SetRecovery` 以零值策略表示清除：零值目前是非法策略，让它变成"清除"会把写错的策略静默当作清除。
- 总在 `Open` 时启动协调循环：没有策略的 Host 也要每 25ms 取操作锁、读目录。
- 只清除运行中登记的策略、保留 `Open` 时策略的 `missing` 语义：同一个 Host 里两种生命周期的策略，宿主要记住每条策略的来历。

### 最近一次失败与信任拒绝

- `RecoveryInfo.Error`：协调循环每次看到 entry 为 Failed 时记下 `Status.Error`；在同一周期内保留（实例短暂 Active 不清除，与预算一致），新周期（`resetRecoveryLocked`）清除。
- 拒绝的识别：`hostInstance.start` 中 `Store.Snapshot` 以 `denied` 失败时，在本次激活上记下"被存储拒绝"（`hostInstance.refused`，`begin` 时清除）。只有 Host 自己的信任或完整性校验能走到这里，插件报告的 `denied` 不会。协调循环对已清理、无清理错误的 Failed 先看这个标记：置 `RecoveryDenied`，不安排重试，不消耗预算。
- `denied` 与 `exhausted`、`cleanup_failed` 一样在本周期内终止；人工 `Retry`、`Update`、`Resume`、重新登记或 `SetRecovery` 开始新周期。批准包（`UpdateTrust`）不自动重试，宿主批准后调用 `Retry`。

拒绝的备选：

- 按 `Outcome.FailureCode == denied` 判断：会把插件报告的 `denied`（如外部服务拒绝凭据，修正后重试可能成功）也当成不再重试。
- 再把 `incompatible`（协议或必需特性不符）等也列为不重试：它们同样是 Host 侧对固定包的判断，但这次没有宿主遇到，代码里也没有统一的分类；只做信任拒绝，并在文档中写明范围。
- 在 `packagehost` 增加导出的哨兵错误：要改 `TrustPolicy.verify` 的错误构造与文本，影响面大于一个内部标记。
- 用 `exhausted` 表示拒绝：宿主会误以为预算已用尽、重试过。

## Risks / Trade-offs

- [已有 `Options.Stderr` 回调会多收到一条没有数据的记录] → 只读 `Data` 的回调收到空数据，不受影响；按记录计数的回调需要识别 `End`。CHANGELOG 标为行为变化。
- [`End` 依赖读取 goroutine 能结束] → 回调或读调用永远卡住时没有 `End`；这在设计中写明，拆除本身仍按既有的 grace 有界。
- [`Remove` 清除 `Open` 时的策略] → 此前删除再同 ID 启用会沿用策略的宿主需要在启用后调用 `SetRecovery`；CHANGELOG 给出改法。
- [`Resume` 现在会阻塞到启动结果] → 与恢复未失败 entry 的路径一致；不想等待的调用方用有期限的 ctx，取消只结束等待，重启已提交。
- [信任拒绝的标记属于激活] → 拒绝发生在新激活的 `begin` 之后，`Restart` 前的旧标记会被 `begin` 清除，不会把上一次的拒绝套到这一次。

## Migration Plan

Go API 新增、行为变化与一个导出常量的删除，随次版本 v0.7.0 发布（`docs/stability.md`：修订版本只有修复）。宿主：按 `End` 释放每次激活的 stderr 状态；不再依赖 `Resume` 对已失败 entry 立即返回；删除 entry 后同 ID 重新启用时重新 `SetRecovery`；把 `missing` 状态的处理删去，增加对 `denied` 的处理并展示 `Error`。不涉及存储或协议迁移。
