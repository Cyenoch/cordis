# Verification

## Local implementation

日期 2026-10-30，基线 `1d640b02074db9981a076c483e97999b5b11ec87` 之后的工作树；darwin/arm64、Go 1.27.3。

- RecoveryPolicy 按持久 native entry 显式配置，Open 校验复制；一个 Host 协调循环提交有界指数退避，不等待某个实例达到 Active。
- 自动重试以核心实际 activation 失败标记、当前登记/identity/revision 和清理结果复核，不从历史身份猜测，不重放业务调用。
- 接受的人工管理失效旧计划；拒绝的 Update/Replace 保留原计划。短暂 Active 不刷新预算，人工 Retry/Update/Resume/重新登记或重开建立新周期。
- CLI 严格加载 JSON/TOML/YAML 策略；前台和在线状态展示恢复计数、计划时间和停止原因。Close 总会提交退役，恢复循环与引擎均结束后才释放 Store/Done。

## Actual checks

- `TestRecovery*` 验证正数/未知 entry 拒绝、默认关闭、策略/快照复制、指数封顶与饱和计算、短暂 Active 仍耗尽、仅指定实例恢复、拒绝变更保持计划、Stop/Resume/Remove/同 ID 重建与重开。
- `TestRecoveryWaitsForCleanupAndIsolatesCleanupFailure` 验证 Quiescing 不启动下一代，清理失败隔离，人工重试仍可用。
- `TestAutomaticRetryDoesNotUseHistoricalActivationForGraphFailure` 保留历史身份后制造依赖环，确认不会被当作可恢复 activation。
- `TestCloseSubmitsRetirementEvenWhenRecoveryJoinTimesOut` 固定恢复线程尚未退出的同步点，确认 Close 超时也已经提交插件退出，Done 不提前结束。
- `TestNativeRecoveryAndOnlineManualRetry` 使用真实签名 native 进程，先以 stats 证明服务可达，再触发 exit(7)，验证有限重试、耗尽、在线人工恢复、健康/Waiting 不受影响、业务错误不重放、Host 重开及连续初始化失败恢复。
- 最终 `bash conformance/run.sh`、`bun run docs:examples`、vet、module verify、文档测试/构建、严格 OpenSpec、schema 互操作、actionlint 与 diff 检查通过。

## Independent review and fixes

Peirce 审查规格时要求区分实际 activation 与历史身份、被拒绝人工变更不改动计划。Hegel 审查实现，发现 Close 在 join 超时后可能跳过引擎退役，已修复并加入同步点回归；同时确认 native 测试的 minor 范围不匹配，已改成准确的 minor 2，先成功调用服务并检查实际退出码。

真实初始化失败还暴露了未完成初始化时发送 retire 的假清理错误，现已跳过该阶段；属于已退出进程的同一通道失败在完整回收后不重复记为清理错误。实际 shutdown/资源清理错误仍保留。全部修正已纳入上述本机全量验收。

## Pending delivery

五平台实际实现 SHA、CI 运行、归档和最终远端一致性尚待交付。
