# Verification

## Batch 1

2026-10-30，工作区基线 `bb29fab02471f80d32d7430a0dd7dad58fff91b0`，未提交的第一批实现。本地实际平台 darwin/arm64，Go 1.27.3。

- `go test -tags=integration -race ./internal/core ./packagehost ./runtime . -timeout 4m` 通过，含真实 native 构建运行、崩溃/意外零退出/强制终止/宿主取消、身份化 stderr 和截断排空；包含动态 child 控制与慢清理查询、旧句柄、Done 与目录释放。
- 新增提交后日志故障注入与大错误历史后执行 `go test -tags=integration -race ./packagehost -run 'TestOperationDiagnosticsReportCommitBeforeSyncFailure|TestRunsRetainRunningAndBoundTerminalHistory' -count=1 -timeout 2m` 通过。
- `go test . -run ExampleHost_Done -count=1` 通过，演示初始化失败定位和带错误的完成关闭。
- `git diff --check` 通过。

独立 reviewer Kepler 对 tasks 1.1/2.1 的公共契约只读审查，提出两项 P2：未知业务错误码不能直接进入固定持久分类；Logger 的同步、及时返回、禁止重入约束需公开。已增加固定类别归一化和大错误重开验收，并在公开注释及中文文档补齐约束。审查未发现需要改变 Outcome、身份化 stderr、子实例来源/owner 或 Done 签名的问题；完整实现仍待最终独立审查。

这不是五平台执行证据；其他平台及最终提交的执行结果留待 task 7.2。

## Batch 2

2026-10-30，仍在同一基线上的未提交工作区；本地 darwin/arm64，Go 1.27.3。

- `go test -tags=integration -race ./cmd/cordis ./packagehost -run 'TestOfflineCLI|TestForegroundHost|TestOfflineIntent|TestOperationDiagnostics' -count=1 -timeout 3m` 通过。覆盖真实目录的签名授权、离线意图、不兼容 schema 的 remove/升级、启用反向依赖、锁冲突、原子停用，以及真实 native 前台恢复/关闭。
- `go test -tags=integration -race ./runtime ./runtime/internal/process ./packagehost ./cmd/cordis -count=1 -timeout 4m` 通过，包含原有 CLI/Build/native 回归。进程树管理已移到共享 internal 包，native 继续使用原有进程组与 Windows Job 路径。
- 为构建补上自有 Go 临时目录后，`go test -tags=integration -race ./packagehost ./cmd/cordis -run 'TestCanceledBuildReapsCompilerDescendants|TestDev' -count=1 -timeout 3m` 再次通过。真实 `go build -toolexec` 的编译工具及后代持有 socket 和输出管道，取消后确认全部退出、临时目录删除；不是只断言 context 已取消。
- dev 真实 native 测试覆盖首次编译失败修复、后续编译/配置/Pack 失败保留旧实例、关闭期间编辑淘汰旧候选、配置快照、初始化失败后继续开发、取消后的清理。另验证旧产物路径改为源码、外部输入/显式配置文件、工具输出不重建、多目标拒绝、临时根布局，以及未完成关闭时目录保留。
- CLI、快速开始和作者文档已补签名安装/启用/前台运行及编辑重启流程，`docs:examples` 已纳入这些实际测试；文档站整体命令仍在最终验收执行。

独立 reviewer Locke 对离线 CLI/前台 Host 提出 P1：默认 SIGPIPE 会绕过 Close 和提交后错误报告。已在 Unix CLI 入口忽略 SIGPIPE，让写入返回错误；真实 CLI 子进程断开 stdout 的测试确认退出码 1、已提交配置保留、Host 终态记录和清理完成。

独立 reviewer Aquinas 对 dev 提出 P1 构建后代未回收，以及三项 P2：临时根内的工作文件自循环、特殊名称的显式配置漏监视、旧产物排除项残留。已分别复用进程树管理并验证实际后代资源释放，拒绝临时根落入监视目录，独立处理显式配置，以及按有效声明替换产物排除集合。对应回归均通过。

以上仍只是本机证据；公开隔离/事件契约、最终独立审查和五平台执行尚未完成。

## Service isolation

2026-10-30，本地 darwin/arm64：

- `go test -tags=integration -race ./internal/core . ./conformance ./examples/pluginmeta -run 'TestIsolation|TestNativeIsolation|TestDocumentationTutorials|TestGenerated|TestMetadata' -count=1 -timeout 3m` 通过。core 覆盖独立组共享公共 key、嵌套缺失不回退、major 分离、同域冲突、旧绑定清理、旧 Caller 失效和跨域等待环不误判。
- 新的真实签名 native 测试覆盖两组同名服务、显式 child 隔离通过会话传递、provider 更新不重启另一组、静态 Restore 重建动态隔离、停用一组不影响另一组，并确认事件仍广播。chain 示例及生成元数据、可执行组合教程已同步。
- 独立 reviewer Turing 审查公开 API、schema 和核心解析，提出 P2：schema 的 major 字符串没有约束 uint32 上限。已修复并增加 0、最大合法值、首个越界值及非规范十进制的共享向量；`go test -race ./contract -run TestIsolationSchemaInteroperability -count=1` 通过。
- 系统 Python 未安装 jsonschema，直接运行检查明确失败；改用已有 `/tmp/cordis-schema-jsonschema-venv/bin/python`（实际 jsonschema 4.26.0）运行 `conformance/validate-schemas.py`，通过 4 份 schema、74 个结构向量和固定签名字节/hash 向量。该 Python 检查不执行 Go codec 或 Ed25519 验签，其输出保留这一限制。

reviewer 未发现最近边界、完整 key、登记 token、旧 Caller/cleanup 权限、provider 索引/等待/环检测/退役或 Restore/child 归属上的其他具体问题。最终完整实现及五平台仍待验收。

## Dynamic subscriptions

2026-10-30，本地 darwin/arm64，Go 1.27.3：

- core 回归覆盖初始化失败不发布、Active 登记、prepend 顺序、显式 Handled=false 值、取消后重新登记、并发 once 只准入一次且失败不重试、自取消与排队投递、退役等待已准入回调、拒绝迟到登记、256 活动名额释放及跨实例事件环。
- `go test -race ./sdk -run 'TestCancelConfirmation|TestUncertainSubscription|TestHostRetires' -count=1 -timeout 30s` 通过。真实内存传输确定性阻塞业务槽，证明 SDK 在取消确认之前已保存收到的投递引用；重复取消和 once 消费会清除登记引用，不确定的已发送控制结果结束会话并清理。
- `go test -tags=integration -race . -run TestNativeDynamicEvents -count=1 -timeout 2m` 通过。真实签名 native 包与 builtin 混合，覆盖三个 dispatch 的逐监听者结果、动态 prepend、self-cancel、并发 once、调用环、错误归属、超大帧的未发送 once 回收、配额释放及失败初始化不发布。
- Host 发送回执的受控会话测试覆盖“取消须等发送确定、无需等回调结束”，包含 once 已消费但仍未发送的竞争。schema/Go 共用规范序列和 uint64 边界向量；JSON Schema 检查通过 4 份 schema、90 个结构向量及固定签名字节/hash。
- SDK native harness 已迁移到独立登记确认及按 ID 投递，counter/chain、typed Subscribe、文档可执行取消与退役示例均使用唯一的新 API。

独立 reviewer James 只读审查 `contract/`、core、Host 桥、wire、SDK 的新契约和生命周期，未发现可确认的具体问题；独立执行 `go test -mod=readonly -race -count=1 ./contract ./internal/core ./sdk ./wire` 以及根包测试通过。该审查没有执行真实 native integration；其范围与主 Agent 的进程验收分开记录。

## Final local acceptance

2026-10-30，本地 darwin/arm64：

- `bash conformance/run.sh` 通过，包含 `go test -tags=integration -race ./... -timeout 5m`，以及独立构建并执行 counter 和 chain 两个真实 Host 程序。
- `go vet -tags=integration ./...`、`go mod verify` 通过。
- `bun run docs:test`、`bun run docs:examples`、`bun run docs:build`、`bun run spec:validate` 通过。docs:examples 实际执行取消示例、离线 CLI/前台 Host、dev、隔离、native 动态事件及原有教程。
- `/tmp/cordis-schema-jsonschema-venv/bin/python conformance/validate-schemas.py` 通过。Python 检查仍不替代真实 Go codec、签名和进程测试。

最终实现的独立审查与五平台 SHA/run/job 证据待后续记录；以上不代表 Linux/Windows 已在本机运行。

## Final independent review

独立 reviewer Nash 对 `bb29fab..19d4185e2c7bd157c74f2d210cce9598422c392a` 做只读实现审查，发现两项 P2：

1. core 移除 child 后、Host 删除元数据前，同 ID 的静态登记可能进入，导致旧动态元数据无法删除。登记预留现在把尚在 `h.dynamic` 中的 ID 视为占用，直到归属元数据删除完成。
2. Host.Remove 用 Package 名称推断 builtin 来源，导致自定义 Package 的 builtin 被误当成持久 entry 删除，可能中断父树移除。现在在停用前保存实际 builtin 身份，按来源决定持久删除。

新增回归先在未修复实现上复现三个失败（移除窗口、独立 builtin、持久父项下的 builtin），修复后执行 `go test -tags=integration -race . -count=1 -timeout 3m` 及 `go vet -tags=integration ./...` 通过。测试控制实际 core/Host 移除间隙，不使用随机 sleep 或生产测试 hook。小范围修正由 Integrator 完成，没有再把已有结论的复查交给其他 Agent。

首份实现提交 `19d4185e2c7bd157c74f2d210cce9598422c392a` 的 [Conformance run](https://github.com/Cyenoch/cordis/actions/runs/40953249643) 已全绿，包含五个真实 native 平台和 contracts；这份结果尚未包含上面两项修正，最终验收以修正后提交的 run 为准。

## Five-platform acceptance

2026-10-30（Asia/Taipei），最终实现提交 `aa8b0858a10274ac2c7a084a96e166d902821282`，包含两项最终审查修正。

[Conformance run 40954607947](https://github.com/Cyenoch/cordis/actions/runs/40954607947) 全部成功；已核对各 job 的实际日志、Go 1.27.3 与目标 OS/arch。POSIX 四个平台均完成 `bash conformance/run.sh`，Windows 实际完成 `conformance/run.ps1`；日志确认 full platform/Windows acceptance、两个独立 native Host 程序以及完整 integration/race 包测试通过。

| 平台 / job | 签名包安装 | 离线 CLI / 前台 Host | native 退出分类 | dev 编辑重启 | 服务隔离 | 动态 child | 动态事件 |
|---|---|---|---|---|---|---|---|
| [Linux amd64](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731329967) | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 |
| [Linux arm64](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731330258) | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 |
| [macOS amd64](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731330194) | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 |
| [macOS arm64](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731330035) | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 |
| [Windows amd64](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731329500) | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 | 通过 |

上述新增功能场景没有失败或未执行项。Windows 不执行 `os.Process.Signal(os.Interrupt)` 子进程投递及 POSIX SIGTERM 专属测试；前台取消、native 清理和进程树回收由 Windows 实际运行路径验证，没有把 Unix signal 填入 Windows 结果。

同一 run 的 [contracts job](https://github.com/Cyenoch/cordis/actions/runs/40954607947/job/123731330211) 通过 schema 和 OpenSpec 验证。[Documentation run 40954607627](https://github.com/Cyenoch/cordis/actions/runs/40954607627) 构建与 GitHub Pages 发布成功。修正后本机再次执行完整 `bash conformance/run.sh` 通过；补充固定官方版本的对照及文档入口后，docs:test/docs:build 再次通过。

归档提交只同步主规格、证据和文档，不追加 Go 行为改动；五平台实现证据对应以上完整 SHA，不以先前基线或首轮候选替代。
