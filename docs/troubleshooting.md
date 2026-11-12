---
title: 故障排查
description: 从实例状态和错误定位依赖、启动、调用与清理问题。
---

# 故障排查

先看实例的 `Status`（`State`、`Waiting`、`Error`、`Outcome`），再对照下表。

| 现象 | 检查 |
|---|---|
| Waiting | `Status.Waiting` 里的服务版本范围、可见 provider 与不兼容原因，或父实例是否已发布；包依赖与服务依赖分开看 |
| Starting 不结束 | `Start` 是否在等自己的子实例或循环调用；是否在 `Start` 里消费流；检查 context 期限 |
| macOS 上 native 启动慢 | 每次激活从新复制的可执行文件启动，系统首次运行一个新文件要先检查它：空闲时约 0.2 秒，机器很忙时观察到超过 10 秒。`cordis run`/`dev` 用 `-startup-timeout` 放宽 |
| Failed | `Status.Error`、`Outcome.FailureCode` 与 `Wait` 返回的错误；按 activation 关联日志，修正后显式 `Update` 或 `Retry` |
| 重复 provider | 同一个解析空间里同 key 的服务不能被两个激活同时提供（`conflict`） |
| 配置更新被取代 | 后续有效更新接管了期望 revision，检查 `UpdateResult.Superseded` |
| Quiescing 不结束 | 用 `Instances` 查看动态子项及 `Owner`，定位未返回的回调、后台工作或清理；取消等待不会释放它们 |
| 包已安装但不能启用 | 是否显式 `Select`、平台是否匹配、必需的包依赖是否已选中 |
| 单次结果超限 | 缩小或分页返回值；完整编码帧必须符合 `frameBytes` |
| 调用 `timeout` | 默认期限 30 秒、上限 120 秒；长流需要定期产出心跳项；链的执行要给 `Do` 的 context 设期限 |
| 链的某一跳 `not_ready` | 链的每一跳占会话的一个在途请求，直到整次执行返回，没有空位时立即失败而不排队；减少并发执行数或缩短链。普通调用在业务名额（在途上限减 8，默认 56）用完时是排队而不是失败；需要更多并行时调高 Host 的 `Inflight` |
| store `in use by another process` | 另一个进程（通常是正在运行的 `cordis host`）持有该 store；Host 运行期间用 `cordis control` 管理，或先停止它 |
| 会话关闭 | stdout 是否混入日志；帧格式、身份与方向是否正确 |
| Linux 无法确认进程组清理 | 检查进程权限与 `/proc` 挂载；启用非零 hidepid 或无法查询时会明确报错，不会把看不见后代当作已退出 |

初始化失败时，结构化日志记录 Starting 到 Quiescing/Failed 的转换，含 entry、activation、启动耗时和错误类别，不记录成功发布。意外的 exit 0 是 `unexpected_exit`；主动停止是 `requested_stop`，`Forced` 另行表示是否升级为强制终止。清理失败保存在独立的 `CleanupCode`，不覆盖退出原因。

`cordis run`、`cordis dev` 直接把插件 stderr 带 `[entry]` 前缀写到 stderr，`cordis host` 需加 `-plugin-stderr`。自建 Host 排查 native 启动时，用 `Options.Stderr` 收集带 `Owner` 的 `StderrRecord`；`Truncated` 表示该激活已超过转发预算，管道仍继续排空。转发失败会返回错误。错误用 `errors.Is` / `errors.As` 分类；框架不会自动脱敏用户返回的错误文本。
