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

## Verified implementation

2026-10-30，最终实现提交 `e2181fab165b0571c83e85a1d6646060b18d9c3a`。[Conformance](https://github.com/Cyenoch/cordis/actions/runs/41012551756) 的五个平台均在对应原生 runner 上执行 integration/race、真实 native 验收、vet 和 module verify；包含多包开发、本机控制和有限恢复，不是交叉编译结论。Contracts job 的 schema 与严格 OpenSpec 检查通过。

| 平台 | CI job | 结果 |
|---|---|---|
| macOS amd64 | [123921460216](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460216) | 通过 |
| Linux arm64 | [123921460484](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460484) | 通过 |
| Linux amd64 | [123921460568](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460568) | 通过 |
| macOS arm64 | [123921460661](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460661) | 通过 |
| Windows amd64 | [123921460717](https://github.com/Cyenoch/cordis/actions/runs/41012551756/job/123921460717) | 通过 |

同一提交的[Documentation](https://github.com/Cyenoch/cordis/actions/runs/41012551777) 通过文档测试、可执行示例和构建，并已部署 GitHub Pages。

## Release linkage

本项实现已包含在固定提交 `e2181fab165b0571c83e85a1d6646060b18d9c3a` 的 [v0.1.0-alpha.1](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.1) 中。[Release workflow](https://github.com/Cyenoch/cordis/actions/runs/41013139380) 再次通过五平台真实运行；下载资产核验记录见[发布验收](../2026-10-30-prepare-native-plugin-release/verification.md)。

## Archive

2026-10-30：6/6 项任务完成，主规格已同步并通过严格校验，按 release → dev → control → recovery 的依赖顺序归档。归档及证据通过后续文档提交交付；发行 tag 保持指向已验收的实现提交。
