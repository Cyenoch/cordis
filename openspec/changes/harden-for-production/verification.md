# 验证记录

## 场景与测试

| 场景 | 测试 |
|---|---|
| 调用期间反复停用再启用提供方 | `hosting` `TestCallsAndStreamsWhileProvidersComeAndGo`、`TestProviderGoneIsACodedError` |
| 对端发送任意内容后断开 | `wire` `FuzzPeer` |

其余：`wire` `TestSessionSurvivesChurn`；`schema` `FuzzValidate`；`contract` `FuzzDecode`；`loader` `FuzzParse`。

## 发现

- 压力测试发现调用方在提供方停止的瞬间收到未分类的 `EOF`（约 15 轮出现一次），已修复；修复后 `-race -count=20` 通过。
- 模糊测试未发现崩溃或停住。

## 实际运行

macOS 15 arm64，Go 1.27.3：

| 模糊测试 | 时长 | 执行次数 |
|---|---|---|
| `wire` `FuzzPeer` | 60 秒 | 约 224 万 |
| `schema` `FuzzValidate` | 45 秒 | 约 442 万 |
| `contract` `FuzzDecode` | 30 秒 | 约 516 万 |
| `loader` `FuzzParse` | 30 秒 | 约 212 万 |
| 既有的 `FuzzFrame`、`config` `FuzzParse`、`FuzzManifest`、`FuzzPackagePath`、`FuzzVersionInRange` | 各 15 秒 | 通过 |

- `bash conformance/run.sh` 通过。CI 只运行模糊测试的种子。
- 测试卡死：推断为 `go mod tidy` 访问校验和数据库时停住，未能复现证实；测试子进程已关闭校验库。
- 五平台 CI：见提交后的运行记录。
