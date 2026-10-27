# Design

## Context

`process.Start` 的拆除 goroutine 在回收进程树后依次关闭 transport、终止/杀死进程树，然后 `cleanupPipes()` 关闭全部管道，最后才 `<-stderrDone`。stderr 读取 goroutine 若仍在处理上一段（例如宿主诊断 sink 较慢），关闭读端后其下一次 Read 得到 `os.ErrClosed`，被视为正常结束，管道缓冲中已写入的数据丢失。`Session.Close` 等待 `done`，因此调用方以为 stderr 已完整交付。

同一读取 goroutine 对每次 Read 都调用 `Launch.Stderr`，`StderrBytes` 只在累计超限时关闭 `stderrExceeded` 触发终止；终止及 grace 期间的输出仍全部转发。

CI 失败日志中 WASM 路径的 `wait for instance: context deadline exceeded` 来自 `cordis run` 的 5s 默认期限；`wasm.Options` 默认 `CompileTimeout`/`StartTimeout` 均为 30s，本机 Apple Silicon 上单次 WASM run 约 1.4s（native 约 0.34s），差值主要为 wazero 冷编译。

## Decisions

拆除顺序改为：进程树回收并 kill 后，用 grace 计时等待 `stderrDone`；到期才关闭 stderr 读端并等待读取 goroutine 返回，然后再执行原 `cleanupPipes()`。被管理的进程树已全部退出，写端随之关闭，正常路径读到 EOF 即结束；只有逃逸出 process group/job 的后代仍持写端时才走强制关闭，最长多等一个 grace。grace 已是宿主为 activation 退出给出的上限，不引入新配置。

回调错误不再中止读取：首个错误后停止转发并保留到读取结束时返回，读取继续计数与 drain，使超限终止及 EOF 仍然生效；宿主 Host 已把该错误记入 `DiagnosticErrors`，诊断不改变会话失败策略。拆除到第一个 grace 仍未读完时关闭读端，再等待一个 grace；读取 goroutine 仍卡在宿主回调内时放弃等待并在 `Err` 记录 `stderr reader did not finish within grace`。被放弃的回调只是那一次在途调用，读端已关闭，之后不会再有新的回调，这与“Done 不承诺 join 任意宿主代码”的既有约束一致。

读取端只把 `min(n, StderrBytes-total)` 字节转发给 `Launch.Stderr`，之后继续读取以保持排空和超限终止，但不再转发。这样排空到 EOF 不会扩大宿主诊断量，同时使“限额采集”成为实际边界。

CLI 默认启动期限取 30s，与 `wasm.Options.CompileTimeout` 默认值相同：本地 run 不应比 runtime 自身更早放弃一个默认配置下合法的冷编译。不在 CLI 增加持久编译缓存，因为那会引入新的用户级可写状态与信任边界；宿主需要更快的重复启动时使用已有 `CacheDirectory`。

测试修正：`go list` 的 `Module.Replace.Path` 保留 go.mod 原文，测试写入 `filepath.ToSlash(repo)`，比较也用同一形式。host DX 测试先在 `t.Context()` 下构建作者包，再为生命周期设置 1 分钟期限；WASM adapter 使用测试私有的 `CacheDirectory`，与重复启动同一模块的生产宿主配置一致。

## Alternatives

- 仅在测试中传 `-startup-timeout`：掩盖普通机器上同样会出现的 WASM 启动失败，拒绝。
- 拆除时无限等待 stderr EOF：逃逸的后代可永久挂住 Close，拒绝。
- 把 stderr 改为同步写到宿主后再允许进程退出：无法约束原生进程，且改变运行模型，拒绝。
- 增大所有测试期限：不能修复 stderr 丢失这一真实缺陷，拒绝。

## Validation

先以真实 native 与 WASM fault fixture 建立回归：慢 stderr 回调下进程在两次写入后退出，断言最后一行被交付；以 1024 字节限额灌入 stderr，断言转发不超过限额；回调首次即失败时仍因超限终止并保留回调错误；回调永不返回时会话在有界时间内结束并报告。四项在旧实现上实际失败，修复后通过。随后运行受影响包的 integration/race、全仓 conformance、vet、规格严格验证和文档检查，由非作者 Review 拆除顺序。Windows 与 Intel macOS 结果以推送后的远程 CI 为准，本机不声称覆盖。
