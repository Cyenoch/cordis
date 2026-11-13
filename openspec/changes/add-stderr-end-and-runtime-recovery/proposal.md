## Why

嵌入 Cordis 的宿主（第三方 native 插件的 mod 管理器，基于 v0.6.0）遇到四处缺口：

1. **stderr 何时结束没有类型化信号。** 宿主为每次激活保留一个行缓冲，用来脱敏密钥；流结束或在每次激活的字节预算处截断时，它要丢掉未写完的最后一行。现在唯一的依据是 Logger 里 "activation stopped / canceled / failed" 的文本，而且这条日志是否晚于最后一条 stderr 记录，取决于排空是否超时。
2. **`Resume` 在重启完成前返回。** 临时停止的 entry 若处于 Failed，`Resume` 调用内核 `Restart` 后立即返回：运行门刚重新加载、尚未提供，内核把实例置为 Waiting 后就不再驱动，`Restart` 的等待随即结束。重启随后失败也不会报告给调用方（v0.5.0 起如此）。
3. **运行中新建的 entry 无法获得自动恢复。** `Options.Recovery` 只在 `Open` 时读取，且要求 entry 已保存；宿主运行期间安装并启用的插件，要到下次启动才有恢复策略。
4. **看不到恢复为什么在等，也会对注定失败的错误反复重试。** `RecoveryInfo` 不带错误；信任校验拒绝（`denied: package … requires an authorized signature or an approved exact digest`）在策略允许的次数内被一再重试。

## What Changes

- `contract.StderrRecord` 增加 `End`：每次激活的 stderr 排空结束时，以一条 `End: true`、不带数据的记录收尾。它总是该激活的最后一条记录，与数据记录出自同一个 goroutine，之后不会再有记录；截断之后照样在流结束时送达。回调此前返回过错误（转发已停止）时不送 `End`；排空超时时 `End` 可能晚于激活结束送达，回调一直不返回时不送达。`cordis run`/`dev`/`host -plugin-stderr` 在 `End` 处补上未写完的行尾。
- **行为变化**：`Host.Resume` 恢复一个已失败的临时停止 entry 时，等到重启有了结果才返回，并返回重启失败的错误，与恢复未失败 entry 的路径和 `Retry` 一致。
- 新增 `Host.SetRecovery(ctx, id, policy)` 与 `Host.ClearRecovery(ctx, id)`：运行中为已保存的持久 native entry 登记、修改或清除恢复策略，校验与 `Options.Recovery` 相同；登记或修改开始一个新的恢复周期。
- **行为变化**：`Host.Remove` 清除被删除 entry 的恢复策略（无论来自 `Open` 还是 `SetRecovery`）；同 ID 重新启用的 entry 不再沿用旧策略。**破坏性变更**：`RecoveryMissing` 状态随之无法出现，删除该常量。
- `RecoveryInfo` 增加 `Error`：本恢复周期内最近一次失败的错误文本。
- **行为变化**：启动时包被存储的信任或完整性校验拒绝（错误码 `denied`，来自 Host 自己的校验，不是插件报告的错误）时，恢复不再重试，状态为新增的 `RecoveryDenied`（`denied`），`Error` 给出原因，预算不消耗。代码中没有现成的"重试也不会成功"错误分类，本变更只针对这一类。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-observability`："显式插件输出与栈采集"增加每次激活 stderr 的结束记录。
- `framework-composition`："可观察控制"要求恢复（Resume）等到重启结果并报告失败。
- `framework-recovery`："显式按实例启用恢复"允许运行中登记、修改、清除策略并在删除时清除；"有限指数退避"对信任拒绝不重试；"恢复状态和关闭可观察"增加最近一次失败与 denied 状态。

## Impact

- 代码：`contract/diagnostics.go`（`StderrRecord.End`）；`runtime/internal/process/process.go`（stderr 排空结束时送 `End`）；`hosting/host.go`（`Resume`、`Options.Stderr` 注释）；`hosting/recovery.go`（`SetRecovery`、`ClearRecovery`、`Error`、`RecoveryDenied`、删去 `RecoveryMissing`）；`hosting/management.go`（`Remove` 清除策略）；`hosting/bridge.go`、`hosting/instance.go`（记录启动被存储拒绝）；`cmd/cordis/plugin_output.go`（在 `End` 处收尾）。
- 文档：`docs/host-guide.md`、`docs/recovery.md`、`docs/runtimes.md`、`docs/operations.md`、`docs/local-control.md`（resume 一行）、`docs/api.md`、`CHANGELOG.md`。
- 兼容边界：会话协议、包格式、契约 schema 都不变；`StderrRecord` 与 `RecoveryInfo` 是 Go 类型（本机控制的 status JSON 多一个可选 `error` 字段、可能出现 `denied`、不再出现 `missing`）。按 `docs/stability.md`，这些是 Go API 的新增、行为变化与一个导出常量的删除，随次版本（v0.7.0）发布，不进修订版本。
- 非目标：不把恢复策略持久化；不在 `UpdateTrust` 后自动重试被拒绝的 entry（宿主批准后调用 `Retry`）；不为插件报告的 `denied` 或其他错误码停止重试；不新增本机控制的策略命令。
