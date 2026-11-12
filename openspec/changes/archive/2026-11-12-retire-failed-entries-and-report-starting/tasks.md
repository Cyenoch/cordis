## 1. 停止已失败的实例（hosting/instance.go 与 hosting 测试）

- [x] 1.1 先写失败测试：`TestRetiringAFailedEntrySucceeds`（`Remove`、`Disable`、临时 `Stop` 已失败 entry）在未修改代码上因 `plugin_failed: broken on purpose` 失败；`TestStoppingAFailedDynamicChildSucceeds` 同样失败；`TestDisableReportsWhatGoesWrongWhileStopping` 的临时 `Stop` 部分因 `stop reported no error` 失败（`Disable` 部分原本就通过，作为不吞错误的保护）
- [x] 1.2 `retire` 与动态子实例的 `halt` 经 `stopForGood`，持久 entry 的临时 `halt` 经 `settled`，二者用 `since` 剔除停止前已记录的失败；上述测试通过（`go test -race ./hosting/`）
- [x] 1.3 逐个调用方决定并写入 design：`Disable`、`Remove`、`Replace`、`Instance.Stop`、guest 子实例 stop 采用新语义；`Host.Close`、加载器 `loaded.Stop`、`Update` 不改
- [x] 1.4 更新断言旧行为的测试：`TestGuestKernelFailures`（`Disable` 已失败 entry 现在成功）、`cmd/cordis` 的 `TestLocalControlAcrossProcesses`（stop/disable 已失败 entry 为 `completed`）；`lifecycle-smoke` 只改注释
- [x] 1.5 审查发现 `errors.Is` 按错误码匹配 `*contract.Error`、会吞掉同码的新失败：先写失败测试 `TestStopResultKeepsANewFailureWithTheSameCode`（未修改时报 `a new failure with the same code = <nil>`），改为按错误值身份沿 `Unwrap` 链比较后通过

## 2. 启动中的状态（hosting/instance.go 与 hosting 测试）

- [x] 2.1 先写失败测试：`TestStatusRightAfterEnableIsStartingNotReasonlessWaiting` 在未修改代码上报 `plain0 waits without a reason`
- [x] 2.2 `Status` 先算等待原因，再定状态：无原因的内核 Waiting 报 Starting，`Applied` 按报出的状态推进；测试通过
- [x] 2.3 受影响的 7 个测试 `-race -count=30` 连跑通过

## 3. 文档

- [x] 3.1 `docs/host-guide.md`：停止类操作的返回值与 `starting` 状态；`docs/local-control.md`：去掉"Stop 成功却返回历史失败"
- [x] 3.2 `CHANGELOG.md` 新增"未发布"一节，按 **行为变化** 标记；`docs/contracts.md` 讲服务契约，不涉及，未改

## 4. 收尾

- [x] 4.1 `openspec validate retire-failed-entries-and-report-starting --strict`
- [x] 4.2 `gofmt -l`、`go vet -tags=integration ./...`、`bash conformance/run.sh`（含 `go test -tags=integration -race ./...`）、`bun run spec:validate`
- [x] 4.3 非作者独立审查（一项 MEDIUM 已在 1.5 修复，三项 LOW 已改文档、测试与 design 风险）
- [x] 4.4 修正后的复审

## Workflow follow-up

- Integrator 审查、发布后归档本 change。
