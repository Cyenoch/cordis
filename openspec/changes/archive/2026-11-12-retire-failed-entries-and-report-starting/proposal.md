## Why

嵌入 Cordis 的宿主（VortexGo 的插件宿主，v0.4.0 起，v0.5.0 未变）遇到两处 `hosting.Host` 行为：

1. 已经 Failed 的 entry 被停用或删除时，停止返回它当初失败的错误。`Host.Disable` 已把 entry 保存为停用并停下，却返回非 nil，调用方分不清"停用失败"与"停用成功、它之前就坏了"；`Host.Remove` 遇到这个错误提前返回，持久 entry 留在原处，要再调一次 `Remove` 才删除。
2. `Enable` 刚返回、内部运行门（run gate）尚未就绪时，`Status` 报 Waiting 且 `Waiting` 为空。嵌入方只能把"没有原因的 Waiting"特判为启动中。

## What Changes

- **行为变化**：停止一个已经 Failed（或已记录失败、正在为它清理）的实例不再返回该失败。`Host.Disable`、`Host.Remove`、`Host.Replace`、`Instance.Stop`（持久 entry 的临时停止与动态子实例）在停止本身没有出错时返回 nil；早先的失败留在 `Status.Outcome` 与运行历史（`Store().Runs()`）中。停止过程中真正发生的错误（清理失败、期限到达、存储写入失败、运行门停止失败）照常返回。`Remove` 因此能删除坏插件的持久 entry。
- **行为变化**（修复）：持久 entry 的临时 `Stop` 现在返回本次停止中出现的清理失败。此前它在停止前取下旧失败返回，却丢掉停止过程中的清理错误，正好相反。
- **行为变化**：实例的 `Status` 在没有任何可报告的等待原因时（`Enable` 或 `Resume` 后运行门尚未就绪、内核尚未处理该实例或正在评估依赖条件）报 `Starting`，且 `Applied` 不前进；`Waiting` 状态总带至少一个原因。
- 不变：`Host.Close` 仍返回关闭时仍为 Failed 的实例的错误（规格"关闭完成与清理错误分离"明确要求）；配置文件加载器的 `package:` 条目句柄 `Stop` 仍沿用内核 `Instance.Stop` 的语义（加载器只记录日志）；内核 `cordis.Instance.Stop`/`Status` 不变。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-composition`: "可观察控制"增加停止已失败实例的结果与启动中状态的报告。
- `framework-control`: "控制结果与取消"中"停止成功但带历史错误"的场景不再成立，改为停止过程中真实发生的错误。

## Impact

- 代码：`hosting/instance.go`（`Status`、`halt`、`retire` 及新的停止辅助函数）；调用方 `hosting/host.go` 的 `retireOne`/`disableEntries`/`replace`、`hosting/management.go` 的 `Remove`、`hosting/children.go` 的子实例 stop 不改代码，随之得到新语义。
- 文档：`docs/host-guide.md`、`docs/local-control.md`、`CHANGELOG.md`。`docs/contracts.md` 讲服务契约，不涉及这两处行为，不改。
- 受益方：`cordis` 本机控制的 stop/disable 对已失败 entry 现在返回 `completed` 而非 `unknown`；`plugintest`、`cordis run` 已把 Starting 当作启动中，不需要改。
- 兼容边界：会话协议、包格式、契约 schema 与 `contract.Status` 字段都不变，`starting` 是已有状态值。改变的是 `hosting` Go API 的返回值与状态取值，按 `docs/stability.md` 属于 Go API 行为变化，建议随次版本（v0.6.0）发布。
- 非目标：不改内核 `cordis` 包的停止与状态语义；不改 `Host.Close`；不为"Waiting 但无原因"新增原因种类。
