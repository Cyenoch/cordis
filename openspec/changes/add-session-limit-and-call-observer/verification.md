# 验证记录

## 场景与测试

| 场景 | 测试 |
|---|---|
| 调用方多于名额 | `hosting` `TestInflightLimitIsTheHostsChoice`（上限 16、默认、200：插件同时看到的调用恰为上限减 8，其余调用方等到名额后完成） |
| 超出范围的上限 | 同上的最后一段；`runtime/internal/process` `TestInflightLimitIsValidatedBeforeProcessCreation` |
| 成功与失败的调用 | `hosting` `TestCallsAreReportedToTheObserver` |

会话 schema 的新字段：`schema/testdata/interoperability.json` 的 `inflight-*` 向量，`conformance/validate-schemas.py`（Python jsonschema 4.26）通过。

## 实际运行

- macOS 15 arm64，Go 1.27.3：`bash conformance/run.sh` 通过。
- 五平台 CI：提交 `feat: host-chosen in-flight limit…` 全部通过，https://github.com/Cyenoch/cordis/actions/runs/43034898421 。
