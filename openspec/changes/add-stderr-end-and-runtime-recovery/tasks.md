## 1. stderr 结束记录（contract、runtime/internal/process、cmd/cordis）

- [x] 1.1 先写失败测试：`runtime` 集成测试中，退出前写下的 stderr、截断后的 stderr、回调卡到排空超时之后三种情形，各收到且只收到一条 `End`、它是最后一条记录；回调失败后不再有 `End`
- [x] 1.2 `contract.StderrRecord.End`；读取 goroutine 在读循环结束、回调未失败时送 `End`，然后才报告排空结束；测试通过
- [x] 1.3 `forwardStderr` 在 `End` 处补行尾并释放该激活的标记；先写失败测试

## 2. Resume 等待重启结果（hosting）

- [x] 2.1 先写失败测试：临时停止已失败的 entry 后 `Resume`，重启失败时返回该错误且返回时为 Failed，重启成功时返回时为 Active；在 v0.6.0 上失败
- [x] 2.2 `Resume` 对已失败 entry 提交 `Restart` 后统一 `instance.Wait`；测试通过

## 3. 运行中的恢复策略（hosting）

- [x] 3.1 先写失败测试：运行中启用的 entry 经 `SetRecovery` 恢复；修改策略开始新周期；`ClearRecovery` 后不再重试；非法策略与未保存 entry 以 invalid 拒绝；`Remove` 清除策略且同 ID 重新启用不沿用
- [x] 3.2 `checkRecovery` 供 `Open` 与 `SetRecovery` 共用；`SetRecovery`/`ClearRecovery`；协调循环按需启动、空表直接返回；`Remove` 清除策略；删去 `RecoveryMissing`；测试通过

## 4. 恢复的最近失败与信任拒绝（hosting）

- [x] 4.1 先写失败测试：包被信任策略拒绝的 entry 在 `Open` 时的策略下不被重试，状态 `denied`、`Error` 含拒绝原因、`Used` 为零；可重试失败的 `Error` 给出失败原因；批准后 `Retry` 成功
- [x] 4.2 `RecoveryInfo.Error`、`RecoveryDenied`；`start` 记录存储拒绝；协调循环据此终止；测试通过

## 5. 文档

- [x] 5.1 `docs/host-guide.md`、`docs/recovery.md`、`docs/runtimes.md`、`docs/operations.md`、`docs/local-control.md`（resume 一行）；`CHANGELOG.md` 未发布一节按 **行为变化** / **破坏性变更** 标记

## 6. 收尾

- [x] 6.1 `gofmt -l`；`go vet ./...` 与 `go vet -tags=integration ./...`
- [x] 6.2 `go test -race -count=3 ./hosting/... ./runtime/...`（含 `-tags=integration` 的 runtime 测试）
- [x] 6.3 `bash conformance/run.sh`；`openspec validate --all --strict`；`bun run docs:test`
- [x] 6.4 非作者独立审查

## Workflow follow-up

- Integrator 审查、发布后归档本 change。
