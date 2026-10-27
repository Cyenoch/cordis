# Tasks

## 1. Worker owner：`cmd/cordis-worker/` 与本 change

- [x] 1.1 核对消费者 trap、固定依赖及官方上游版本，记录根因和最小边界，保留并行工作。
- [x] 1.2 独立审查方案，补充有界读取/无数据/EOF/非法内存/关闭语义回归，并实现 Windows stdin 修复。
- [x] 1.3 执行本机 unit/race/vet、真实 worker/guest 回归和 Windows amd64/arm64 交叉构建；准备准确 Windows 命令与产物摘要。
- [ ] 1.4 非作者独立 Review 修复，只提交本写集并公开发布，交消费者固定 commit 和变更记录。

## 2. Consumer owner：Windows 串行实测

- [x] 2.1 在现用 Windows 11 ARM64 guest 的 amd64 兼容进程运行读取回归与真实大 manifest/归档测试，记录产物摘要和退出状态；未覆盖真实 Windows x64。

本任务不授权本 Agent 修改消费者或控制其 VM；实际运行证据由消费者 owner 交回后再记录，不预勾通过。

## 实际验证记录

### 本机与隔离发布树

- darwin/arm64，Go 1.27.3：worker 的 unit / integration / race / vet 通过，真实 Go WASI guest 在 stdin 空闲时 timer 继续执行，单次 host Write 的约 500 KiB 输入经 4096 字节 buffer 完整读回且 SHA-256 一致，EOF 和宿主取消退出通过。
- 错误域回归以真实 guest 调用观察 ABI；临时 overlay 恢复初版 raw cast 后 AGAIN/FAULT/INVAL/IO/BADF 五项实际失败，修正后 race 20 次通过，不把共享本地常量对比当作 ABI 验证。
- 从已提交 HEAD 导出的隔离发布树仅覆盖本 worker 写集，使用既有 go.mod/go.sum，worker 与 runtime 的 integration/race/vet 通过。并行未提交 contract/SDK/CLI/依赖没有进入该发布树。
- Windows amd64 / arm64 worker 与 test executable 已交叉编译成功；只交叉编译不计实跑。生产错误仍保持脱敏，没有新增包、文件系统、环境或网络能力。

### Windows 最小回归（由消费者 owner 串行运行）

环境为 Windows 11 ARM64、普通用户；amd64 是兼容进程，arm64 为本机原生。消费者核对 SHA-256 后运行：

```powershell
.\worker-amd64.test.exe -test.run '^(TestWindowsStdinRead|TestStdinReadErrorsCrossWASIBoundary)' -test.v -test.count 20 -test.timeout 90s
.\worker-arm64.test.exe -test.run '^(TestWindowsStdinRead|TestStdinReadErrorsCrossWASIBoundary)' -test.v -test.count 20 -test.timeout 90s
```

两个程序均 exit 0 / PASS，四个顶层测试各 20 次通过，包括五个 ABI errno 子测试、积压超过 4096、EAGAIN/EOF、flags 拒绝/清除、非法内存不消费、fd_close 和普通文件重定向。原始 baseline amd64 使用相同 guest wrapper，exit 1，实际复现 `slice bounds out of range [:21000] with capacity 4096`；不重复使用已撤回的初版。

| 产物 | SHA-256 |
|---|---|
| 新 amd64 test | `4325140d82ccbc4e30b2b40f35586d222bcfa9a699f6303359a0a5d4febac1a0` |
| 新 arm64 test | `993d06208c1774bb9a914ca1c35bab201d09d21512bbc1dcfb12c14873ca77bf` |
| 新 amd64 worker | `e6e69533db01b30cf3beaaa1ffa7e610d55d844ffb58236d366fe01f1d074923` |
| 新 arm64 worker | `4fe70e2ba4c5dd681854ba6129fb00401b63a16558d33427185a4b1c6f92ff43` |

执行结果由消费者 owner 回报，并只读核对其 `mvp-cordis-stdin-results-v3.json` 和两份 v3-fixed 日志；私有本地证据不复制进公开源码。未验证原生 Windows x64 OS，不将 ARM64 的兼容进程视为 x64 认证。

### 消费者真实签名 WASI 集成

消费者 owner 在 Windows 11 ARM64 普通用户的 amd64 兼容进程中，使用上表新 amd64 worker，实际运行 `TestSignedStardewWASIPlansRealManifestRulesAndLargeArchive`：PASS，141.98 秒，进程 exit 0。只读核对日志中的生产 worker 摘要与上表一致。消费者冻结源码的独立副本仅替换 worker 构建入口以读取并核对既有二进制，不修改其主源码或 module。

覆盖签名包安装/启用、首次发现/PE 版本规则、Mod inspect 与受控读取、大归档规划完整分页和失败/停用分支。临时游戏布局与合成归档不等于真实游戏部署、启动或产品认证；未由本 Agent 操作 VM。

### 最终独立 Review

非作者实现审查未发现生产路径的可确认正确性或安全问题，确认 errno 映射与固定依赖的错误域一致；另实际运行 unit/race 20 次、worker integration、runtime isolation 和 vet，以及 raw-cast 失败 overlay。发现 P3：Go guest 的 `io.CopyBuffer` 被 `os.File.WriteTo` 绕过，实际使用 32 KiB，不能作为当时 4096 字节回归证据。已隐藏源的 WriterTo 方法并重跑该测试；此修正只改测试夹具，不改变上表已在 Windows 通过的生产 worker。Windows 独立 WASI probe 原本即明确使用 4096 字节，不受该夹具问题影响。

修正测试夹具后，再次在 HEAD 加本写集的隔离发布树执行 `GOWORK=off go test -tags=integration -race -count=1 -timeout 180s ./...` 与 `go vet -tags=integration ./...`，全部通过；worker 单包亦重新执行 integration/race/vet。重新编译 Windows 双架构生产 worker 与上表摘要逐字节一致，证明测试夹具修正未改变消费者已实跑的生产二进制。依赖 verify 与 tidy-diff 无变化。Review 的 P3 小修由本 Agent 处理，未再委派复审。

## Changelog

- 修复 Windows WASI 非阻塞 named-pipe stdin 在可读积压大于 guest buffer 时的 slice 越界；每次读取受 guest buffer 实际长度约束。
- 保留 EAGAIN/EOF、成功 flags 更新/关闭后的 fd 语义、原有 poll/HANGUP 和宿主取消行为；普通文件/控制台与其他 fd 仍交给上游。
- 自定义 fd_read 出口显式映射 experimental/sys 错误域到 WASI preview 1 errno，避免把 EAGAIN 误报权限错误。
- 增加真实 guest 内存、ABI errno、Windows pipe/flags/close 和真实 worker 流读回归；Wazero 版本、公开契约、协议及依赖清单不变。
- 发布仅包含本 worker 与独立 change，不包含并行未提交开发。消费者仍需按固定发布 commit 升级 module 并回归。
