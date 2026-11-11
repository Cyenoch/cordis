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
- 此前偶发的本地测试卡死（`go` 子进程零 CPU 停住）原因未查明。曾推断是 `go mod tidy` 访问校验和数据库，实验否定了它：切断网络后该命令照常完成并写出 `go.sum`。为此加过的环境设置已撤回。
- 五平台 CI（ubuntu-24.04、ubuntu-24.04-arm、macos-15、macos-15-intel、windows-2025，另有 contracts）：提交 `3190432` 全部通过，https://github.com/Cyenoch/cordis/actions/runs/43032432852 。此前两轮各有一个平台失败，都是依赖机器速度的既有测试（`hosting` 的停用超时、`loader` 的连续编辑），已改为不依赖时序。

## 独立审查（运行时）

非作者审查真实进程运行时、恢复与替换的竞态和本变更：1 项阻断，5 项应修。

| 意见 | 处理 |
|---|---|
| 真实进程上调用方仍收到不带分类的传输错误（原判断依赖进程收尾信号，只在内存运行时成立） | 会话层以 `contract.ClosedError` 标出“会话在回答前结束”，宿主按类型归类；验收程序新增真实进程的“负载下崩溃”与“负载下重启” |
| 负载下崩溃被记为 `protocol_failure` | 写入端失败归为对端离开；验收程序检查运行记录为 `unexpected_exit` |
| 放弃等待的 Replace 留下无法恢复的登记 | 清理结束后自动撤销登记；`TestReplaceGivenUpOnLeavesTheEntryRestorable` |
| 宿主被杀后，不响应 stdin 关闭的插件继续运行（Unix） | SDK 在会话结束后至多再运行 `sdk.CleanupLimit`（10 秒）即退出；`TestCleanupThatOutlastsTheLimitEndsTheProcess` |
| Windows 上插件与宿主共用控制台，Ctrl+C 直达插件 | 插件进程建在独立进程组；本机无法运行 Windows，仅由 CI 的 Windows 验收覆盖不回退 |
| 提供方协议失败以其分类到达无关调用方 | 一律 `not_ready` |
| 事件投递绕过归类 | 已归类 |

领头进程回收后才向进程组发信号：已处理。Linux 与 macOS 上，进程树在领头进程退出但尚未回收时结束进程组（此时它的进程号仍被占用），回收之后不再发任何信号；`internal/processtree` `TestWaitEndsTheGroupBeforeReapingItsLeader`。其他 Unix 没有“不回收而得知退出”的途径，行为不变。

未处理，记录在案：`noexec` 的临时目录下插件无法启动（需设置 `TMPDIR`）；请求的响应入队后，沿用该请求 context 的迟到调用会结束会话；`FuzzPeer` 的两个在途请求先后次序不确定；若干既有测试仍含固定时长等待（`wire/stream_test.go:302`、`hosting/chain_test.go:104`、`wire/session_test.go:162`、`hosting/management_test.go:205,228`、`hosting/boundary_test.go:190`）。
