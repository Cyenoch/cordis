# 验证记录

## 场景与测试

| 场景 | 测试 |
|---|---|
| 调用方多于名额 | `hosting` `TestInflightLimitIsTheHostsChoice`（上限 16、默认、200：插件同时看到的调用恰为上限减 8，其余调用方等到名额后完成） |
| 超出范围的上限 | 同上的最后一段；`runtime/internal/process` `TestInflightLimitIsValidatedBeforeProcessCreation` |
| 成功与失败的调用 | `hosting` `TestCallsAreReportedToTheObserver` |

会话 schema 的新字段：`schema/testdata/interoperability.json` 的 `inflight-*` 向量，`conformance/validate-schemas.py`（Python jsonschema 4.26）通过。

## 独立审查

非作者审查一轮：无阻断，4 项应修，5 项次要。

| 意见 | 处理 |
|---|---|
| `Options.Inflight` 的注释与行为相反（写成“拒绝”，实际是等待） | 改正 |
| 协议与排障文档仍写固定的 64，未列 `inflight` | 改正 |
| 会话层不强制新字段，也不检查两端一致 | `wire` 要求该字段，Host 一侧拒绝与自身不同的值，插件一侧在收到 `initialize` 时接管；去掉公开的 `SetInflight`；`wire` `TestInitializeCarriesTheInflightLimit` |
| `native.Options.Limits.Inflight` 被静默忽略；内存运行时不校验范围 | 与 `Launch` 不一致时拒绝；内存运行时同样校验 |
| 没有测试覆盖插件到 Host 方向的非默认上限 | `hosting` `TestInflightLimitBindsThePluginsRequests` |
| 观测测试断言耗时大于零，粗粒度时钟下可能为零 | 改为不小于零 |
| `Duration` 的含义未写明 | 写入运维文档 |
| 互操作向量缺“缺少 `inflight`”一例；帧级示例不完整 | 补上 |

控制请求挤占业务名额（既有行为）：插件一侧的控制请求原先不受预留的 8 个名额约束，并发的退订会把会话占满，使本应等待的请求以 `not_ready` 失败。现在它们在预留的名额内排队；`hosting` `TestControlRequestsDoNotCrowdOutCalls`（去掉修复后该测试失败）。

## 实际运行

- macOS 15 arm64，Go 1.27.3：`bash conformance/run.sh` 通过。
- 五平台 CI：提交 `feat: host-chosen in-flight limit…` 全部通过，https://github.com/Cyenoch/cordis/actions/runs/43034898421 。

## CI 在慢机上暴露的问题

- `loader`：`Ready` 的条件读取“当前是否稳定”，而内核在宣告之后才评估它；评估落在下一次编辑之后时，已经启动的实例会被停掉再启动（macos-15-intel 上出现）。改为单向锁存：条目第一次全部稳定后即满足，此后不变。
- `wire`：模糊测试种子留下的写入期限定时器在后面的测试里触发，被按 goroutine 数量断言的测试计入（ubuntu-24.04-arm 上出现）。写完即撤销期限。
- `cmd/cordis` 的集成测试在 macos-15-intel 上超过整包 5 分钟的时限；堆栈显示它仍在运行而非卡死。时限放宽到 10 分钟。
