# Tasks

## 1. Runtime owner

- [x] 1.1 归类远程 Conformance 4 次运行的失败，区分运行时缺陷、CLI 默认值与测试错误，冻结最小方案。
- [x] 1.2 以真实 native/WASM fault fixture 建立 stderr 末尾丢失与转发超限回归，确认旧实现失败后修复拆除顺序与转发限额。
- [x] 1.3 对齐 `cordis run` 默认启动期限与文档，修正 Windows replace 路径比较、host DX 生命周期期限与 CLI 默认值测试。
- [x] 1.4 执行本机 integration/race、conformance、vet、规格与文档门禁，由非作者 Review 拆除顺序。
- [ ] 1.5 推送后核对五个远程平台的 Conformance 结果。

## 实际验证记录

### 失败归类（远程 CI）

运行 40495703695、40496050260、40513305348、40513613020 的 runtime job，自 e6e5f17 起持续失败：

- WASM `cordis run` 在 ubuntu-24.04、macos-15、macos-15-intel、windows-2025 报 `start definition "main" (timeout 5s): wait for instance`；windows-2025 一次 native 运行同样超时。
- host DX WASM 子测试在 30s 期限内超时，其中一次为 `build author: signal: killed`。
- windows-2025 `TestBuildActualSDKNativeAndWASMWithoutRunningArtifact` 每次失败：`go list` 的 `Replace.Path` 保留 go.mod 原文 `D:/...`，测试与 `D:\...` 比较。本机以相对 replace 证实 `Replace.Path` 不规范化。
- macos-15-intel 一次 native `real process did not cancel work before cleanup: "managed-canceled\n"`：缺失的 `cleaned` 由拆除时先关闭 stderr 读端造成。

### 本机（darwin/arm64，Go 1.27.3）

- 新回归 `final-stderr-before-exit` 与 `stderr-forwarding-bounded` 在旧 `process.go` 上 native、WASM 均实际失败（只收到 `first-diagnostic`；转发 1500–2260 字节超过 1024 限额），修复后 `TestExternalRuntimeIsolation` 连续 5 次通过。
- host DX 生命周期测试 3 次通过；CLI 测试改为验证 help 中 `(default 30s)` 与显式 2s 期限下的有限失败及诊断，不空等默认期限。
- `go vet -tags=integration ./...`、`go mod verify`、`go mod tidy -diff` 通过，module 无变化。
- `go test -tags=integration -race -count=1 ./... -timeout 10m` 全部通过，未复用测试缓存；`bash conformance/run.sh` 输出 `Full platform acceptance passed.`。
- `openspec validate --all --strict` 6 项通过；`docs:test` 6/6、`docs:build` 与链接检查通过。

Windows、Intel macOS 与 Linux 的结果以推送后的远程 CI 为准，本机未覆盖。

### 独立 Review

非作者只读审查未发现拆除顺序的新死锁：所有路径在回收进程后才进入排空；Windows `tree.close()` 先等待 job 排空。临时副本中以 setsid 后代保持 stderr 写端，Done 在 grace 300ms 后约 331ms 到达且两行均交付；两项新回归在旧实现上各 6/6 失败。审查确认的阻塞项（CLI 测试仍假定 5s 默认）及规格须写明 grace 上界、注释措辞两项已修正后重新验证。

遗留且非本 change 引入：诊断 sink 永不返回时 `Close` 仍等待读取 goroutine；sink 返回错误后读取停止，持续写 stderr 的插件可能阻塞在满管道上而不触发超限。另行处理。
