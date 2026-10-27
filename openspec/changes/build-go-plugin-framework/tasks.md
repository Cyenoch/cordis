# Tasks

当前源码写集相对于 Cordis 独立仓库根。本 change 随框架整体迁移，MIT 不变，不复制平行规划源。下方第 1–10 组及平台表原样保留迁出前的任务、旧路径、旧模块名和运行事实，不表示重命名后已重新验证；当前布局以 design 为准，迁出验证单独记录。

协作：Integrator 独占根公开契约、`go.mod`/`go.sum`、`contract/`、`wire/`、`schema/` 与 OpenSpec。其他写集使用独立 worktree，不同时修改共享文件；契约变更交 Integrator 合并。每组自带其行为测试、作者文档和真实 smoke，不能把这些推迟到最后。任务全部未完成前不能把 core-only 或单运行时版本称为本 change 已交付。

依赖：1 完成后推进 2.1–2.4、3/4/5 与 8.1；2.1–2.4、3、8.1 提供契约和核心入口后推进 8.2；2.1–2.4 与 8.2 的会话入口可用后推进 6/7。3/4/5/6/7 汇合后由 Integrator 完成 2.5，再推进 8.3/8.4/9，10 验证完整产品。8.2 先以真实 stdio peer 验证 SDK，不等待 Runtime Adapter；WASI command 的实际装载与双向运行由 6.3 验证，避免 SDK 与 Runtime 互等。公共契约修正由 Integrator 处理。

## 1. Integrator：独立落点与公共契约

- [x] 1.1 在 `cordis-go/` 建立独立 module 落点并保留唯一 change 与固定 Cordis 参考引用；验证 OpenSpec 可读取本 change，框架不导入 VortexGo 根 module 或内部包。
- [x] 1.2 建立 `github.com/cyenoch/vortexgo/cordis-go` module 与 MIT/第三方声明，审查 wazero v1.12.0 及传递依赖许可证；验证 `go mod download` 成功并交付可追溯清单，不创建远端或发布。
- [x] 1.3 定义公开 Scope/Definition/Entry/Identity/Runtime、archive/ImportDirectory 包来源、MetadataDecoder 与宿主策略 seam，以及稳定错误和配置结果；用公开消费示例验证多实例、取消结果与调用者错误处理可表达，交付 Interface 文档，不开放宿主内部对象。
- [x] 1.4 在 `schema/`、`contract/`、`wire/` 定义 Manifest、签名输入、会话和受限数据契约的完整 schema 与互操作向量；验证边界版本、64 位整数、nullable、错误与方法方向向量可被独立读写，字段和签名字节布局无歧义。

## 2. Integrator：会话与数据契约 Implementation

- [x] 2.1 实现严格、有界的 NDJSON codec、关联表、双向 ID、版本/特性协商及协议错误关闭；运行会话行为测试，验证畸形、重复键、超长帧、未知关联、方向错误和并发双向请求均不误入 handler，并编写协议作者文档。
- [x] 2.2 实现请求 deadline、取消/响应线性化、迟到结果拒绝与控制消息独立读取；运行可控时钟和真实 pipe 双向调用 smoke，验证取消后不会提交迟到结果、堵塞业务时 shutdown 仍可推进。
- [x] 2.3 实现远端服务路由的 binding、宿主 call-chain 和等待图；验证同链 A->B->A 及独立根调用交叉 A/B 等待均在排队前拒绝形成环的边，取消/退出会删除边，非循环双向调用可完成，不靠并发进入串行实例绕过。
- [x] 2.4 对 codec/契约输入建立无效输入回归与 fuzz 入口；运行确定性回归并执行一次有界 fuzz smoke，验证失败局限于会话且错误不回显原始敏感载荷，记录实际命令与结果。
- [x] 2.5 在 3/4/5/6/7 就绪后实现根宿主 Facade 的组合、启动/关闭与原子发布入口，不引入反向 import；通过公开入口运行真实外部插件的加载/调用/停用 smoke，验证 Package Host、core、access 与两个 Runtime 使用同一 activation 所有权，并交付宿主接入文档。

## 3. Core owner：`internal/core/`

- [x] 3.1 实现定义、多实例、activation 状态与串行控制协调；运行行为测试和公开入口 smoke，验证旧启动结果不能覆盖新激活、实例失败不停止兄弟实例，交付状态/并发 Interface 文档。
- [x] 3.2 实现类型化 service key、契约版本、realm/子 Scope、局部 overlay 和依赖等待；验证晚到 provider 唤醒、取消后不启动、隔离子树不串服务、重复 provider 与动态等待环可诊断。
- [x] 3.3 实现 committed binding、先取消普通在途工作再等待消费者的退役顺序及独立 cleanup context；验证 C->B->A 顺序、在途任务阻塞清理调用可解除、清理绑定不被新业务复用，以及不合作 builtin 仅报告阻塞而不假释放。
- [x] 3.4 实现 effect/group 的幂等关闭、组内逆序等待和错误聚合；验证初始化中途失败、异步清理失败仍尝试其他清理、并发重复 close 不重复释放，文档区分资源清理与业务回滚。
- [x] 3.5 实现 desired/applied revision、前置配置校验、更新收敛与显式重试；用可控启动/卸载暂停点验证 1->2->3、失效配置、取消等待、失败恢复与 superseded 结果，不按延时碰运气。
- [x] 3.6 实现作用域分发、顺序/并行/handled/middleware 与回调准入；验证有效零值短路、next 重复拒绝、聚合错误、停止时在途回调和串行实例规则，并运行 `go test -race ./internal/core/...` 及 goroutine 泄漏检查。

## 4. Package owner：`packagehost/`

- [x] 4.1 实现 ZIP 和显式开发目录到不可变快照、Manifest 校验、平台选择与候选诊断；用真实合法包及平台不匹配包 smoke 验证没有代码执行，交付包格式/发现文档。
- [x] 4.2 实现规范化路径、冲突/链接拒绝及实际读取配额；运行根外路径、大小写冲突、保留名、损坏 ZIP、膨胀和取消 fixtures，验证受管目录之外没有写入，并建立解析器 fuzz 入口。
- [x] 4.3 实现完整性表、确定性摘要/Ed25519、key-publisher-pluginID 授权、显式发布者变更、吊销与 digest 级 dev 授权；用独立签名向量、另一可信发布者提交同 ID、篡改及吊销 fixture 验证加载前拒绝，并交付信任模型文档。
- [x] 4.4 实现 durable 暂存/事务日志、包含 installed/selected/entry 的原子 catalog、存储锁和恢复；在各提交点注入进程退出，验证可区分提交前后取消，运行 v1 时仅安装 v2 后重启可保留 v1 选择及 v2 未启用安装状态。
- [x] 4.5 实现显式版本选择、必需/可选包依赖预检、entry 启停及按 scope 调用 Runtime；验证同版本不同 digest 拒绝、依赖循环不执行、optional 诊断和同包多实例独立。
- [x] 4.6 实现代码更新、反向依赖范围预检、显式级联停用、旧版本恢复与宿主数据版本确认；运行更新破坏 A->B 版本范围、新代码启动失败、迁移已提交和更新取消 smoke，验证冲突不改变原状态且不自动运行不兼容旧代码，交付恢复指南。
- [x] 4.7 实现卸载依赖阻塞、数据保留/授权删除和 Windows pending removal；验证受管进程停止后代码移除、外部句柄阻塞时不假报卸载成功、重启清理可恢复且不删除其他包。

## 5. Access owner：`access/`

- [x] 5.1 实现 activation 绑定的声明/授权/范围准入与撤销 epoch；用并发准入/撤销测试验证撤销后的请求不进处理器、在途收到取消，运行 `go test -race ./access/...` 并文档说明原生权限局限。
- [x] 5.2 实现暂存贡献批次、宿主 validator、activation snapshot 发布/撤销；验证部分非法时全批不可见、旧 activation 迟到注册拒绝、停止只撤销对应 owner。
- [x] 5.3 实现受管任务唯一终态、取消、宿主显式接管及存储 namespace/quota Adapter；验证停止不伪造任务成功、接管任务存活、跨插件读取和超额写入被拒绝，文档明确 checkpoint/业务迁移由宿主实现。
- [x] 5.4 实现稳定阶段诊断、日志/输出限额与宿主脱敏 sink；运行含秘密的错误与插件日志 fixture，验证导出只包含已批准投影，不默认回显 payload、配置、令牌和完整用户路径。

## 6. WASM owner：`runtime/wasm/`、`cmd/cordisgo-worker/`

- [x] 6.1 用 wazero 实现 WASI command 实例和明确 stdio/clock/random 配置、内存/编译/启动/执行限额；运行真实 Go WASM 无 preopen/秘密环境/任意进程访问 smoke，不能用 reactor 示例或 mock 代替，并编写 Runtime 限制文档。
- [x] 6.2 实现每 activation 独立 worker、健康检查、有界关闭、管道关闭和强制结束；验证无限循环、stdin 无数据阻塞、超量输出、worker crash 均可停止且宿主存活，失败局限当前实例。
- [x] 6.3 接入 Go SDK 独立协议读取与串行业务调度，验证真实 WASI command 中 call->host->response、cancel 和控制消息可推进、重入被拒绝，worker 的 pollable stdin 支持调度，不提供手动 Pump 接口。
- [x] 6.4 实现多实例和显式有限重启策略；运行双实例状态隔离、关闭一个不影响另一个、重启新 activation 拒绝旧消息、预算耗尽停止的真实插件 smoke，记录支持平台的运行证据。

## 7. Native owner：`runtime/native/`

- [x] 7.1 实现无 shell 的精确产物启动、环境/cwd、stdio 会话和退出归属；运行独立 Go 插件服务调用与 panic smoke，验证宿主及其他会话存活，并交付 trusted-native 运行边界说明。
- [x] 7.2 实现 Unix 进程组及 Windows Job Object 的受管树关闭和句柄回收；实际运行 child/hang/忽略取消插件，验证期限后进程树退出、无受管孤儿和死锁，不将交叉编译记为通过。
- [x] 7.3 接入多实例、重启预算、迟到结果和产物快照校验；验证新 activation 不接收旧响应、同包两个实例状态独立、被篡改产物不启动，提供原生分发与卸载说明。

## 8. SDK/tool owner：`sdk/`、`cmd/cordisgo/`、`examples/basic-host/`、`examples/plugins/`

- [x] 8.1 实现受限数据契约的 Go 生成器、类型化 client/server 与字段验证；用独立作者 module 编译并实际互调，覆盖 nullable、64 位整数、业务错误和不兼容版本，拒绝不可序列化类型并交付作者指南。
- [x] 8.2 实现 SDK 的 Scope、资源组、服务/事件、受管任务和两种会话运行入口；运行独立作者插件与真实 stdio peer 的双向调用/取消 smoke，并分别构建 native 与 WASM 产物，验证作者无需手写协议或导入 host internal；真实 WASI 装载证据由 6.3 补齐，不把仅构建记为 Runtime 验证。
- [x] 8.3 实现 pack/sign/validate/inspect/run 命令和明确退出码；按文档从独立 module 构建两种插件、生成签名包并加载，验证错误指向文件/字段、拒绝未知 runtime 和未授权 unsigned，签名工具不回显私钥。
- [x] 8.4 提供可运行非领域宿主及服务 provider/consumer 插件；运行归档安装、双实例、配置更新、provider 退役、版本切换、调用取消和卸载全链路，证明宿主构建时没有链接外部插件代码。

## 9. Integration owner：`conformance/`

- [x] 9.1 固定 VortexGo 三项 spec delta 与 design 契约的来源摘要，构建不依赖 VortexGo/MyGo 的 Manifest/NDJSON Adapter 和 fixture host；运行现有契约正反向消息及 drift fixture，验证不重写宿主 schema、方法和错误码，文档明确这是契约验收而非真实 UI 认证。
- [x] 9.2 使用真实 Go WASM 和独立 worker 验证 VortexGo 的 command/query/event/capability/job 映射、默认拒绝、凭据不可见与裁剪结果；覆盖 job/event 请求确认和超时，不能靠 mock 转发回声通过。
- [x] 9.3 验证宿主贡献 validator、批量发布/撤销、插件崩溃、旧 activation 消息、受管任务取消与已接管事务任务存活；运行完整 fixture host smoke，输出已覆盖契约及明确未覆盖的 MyGo/游戏/真实部署范围。

## 10. Integrator 与独立 Review：完整交付验证

- [x] 10.1 在独立仓库跑全套 `go test -race ./...`、独立作者 module 的检查及公开宿主双运行时 smoke，验证五项 capability 的场景都映射到行为证据；不是只凭编译、单元测试或静态注册演示宣布完成。
- [x] 10.2 在 Windows amd64、Linux amd64/arm64、macOS amd64/arm64 实际执行包安装/恢复、两种 Runtime 调用/超时/停止/卸载及 VortexGo 契约 smoke；交付逐目标命令与输出，不能用交叉编译填补运行证据。
- [x] 10.3 测量进程内分发、IPC 调用、冷启动与 WASM 缓存的延时和分配，运行相应 benchmark 与真实负载 smoke；记录结果和限制，不引入缺乏场景的数字门禁或为追指标改变契约。
- [x] 10.4 由非作者独立 review 公开契约、包/签名、路径、生命周期、取消和 worker 安全面；修复真实发现并运行对应回归，确认五项规格无遗漏后才准备首个稳定发布，发布仍需所有者明确授权。

## Verification Evidence

### 证据约束

以下路径均相对 `cordis-go/`。Go 版本为 1.27.3；工作区没有可引用的有效 HEAD，因此不虚构提交号。每个平台脚本重新构建独立作者 module 和真实 worker；历史构建、交叉编译、静态 review 与实际执行分别记录。VortexGo 相邻规格并行修订时，来源检查实际报 drift，审查并补齐 12 方法、维护模式及精确依赖后才更新摘要。五项 capability 共 49 requirements / 66 scenarios，逐项映射如下；最终目标结果见平台记录。

### 已执行的通用框架行为

`conformance/run.sh` / `run.ps1` 先执行 `go test -tags=integration -race ./... -timeout 5m`，再实际运行 basic-host、原始 ABI worker、签名原始包及 native/WASM lifecycle-smoke。最近通用实现含 shutdown 确认发布/EOF drain、Windows 同步 pipe 与 kill 解耦、Job drain/映像删除重试、无后缀 PE 的 `.exe` 私有副本，以及取消夹具 activation-owned task.begin；未将这些修复前的输出标为通过。

已观察的通用链输出为 native 和 wasm 各自的签名安装、双实例、回调、事件、配置重启、版本切换和卸载；lifecycle-smoke 两种运行时均包含 18 项断言，包括真实 1.0.0 → 独立失败产物 1.2.0 → 显式恢复 1.0.0，不重启宿主。失败版本与旧版本数据版本相同；兼容迁移另由 Store/Namespace 行为测试证明，不把代码恢复当数据回滚。

Windows 已实际运行 `TestStoreUninstallRecoversWindowsHeldArtifact`：真实 CreateFile 句柄不共享删除，卸载记录 pending/error，持句柄重开仍 pending，关闭句柄重开完成清理；另一个已选包和两份数据均保留。`TestExternalRuntimeIsolation` 的 native/WASM loop、block、malformed、stderr、unexpected-zero-exit、close-active-call、explicit-lifetime，以及 native managed-descendant 均实际通过。Windows amd64 在 Win11 ARM64 的 x64 仿真下运行，不是物理 amd64 硬件认证；race 使用临时 ARM64-host llvm-mingw 的 x86_64 compiler，未修改 VM 系统配置。

### 最终平台记录

2026-10-26 当前 12 方法 Adapter、维护分页、直接签名服务绑定与合法逻辑 ID 修复后的完整执行结果如下；全部退出 0。每个目标输出 native/wasm basic-host 各一行成功、原始 ABI 14 项 `checks`、原始签名包 `committedServiceCalls:2` / `retiredAndUninstalled:true`，以及 native/wasm lifecycle 各 18 项断言。故障链预期保留 crash/exit 历史错误，不把它们吞成成功。

| 目标 | 实际执行环境与命令 | 最终记录 |
|---|---|---|
| macOS arm64 | Apple Silicon；`bash conformance/run.sh` | bg_46 / artifact 309；62.79 s；`Full platform acceptance passed.` |
| macOS amd64 | Rosetta；`env GOARCH=amd64 bash conformance/run.sh` | bg_47 / artifact 310；105.42 s；`Full platform acceptance passed.` |
| Linux arm64 | Docker Linux VM；`docker run --rm --platform linux/arm64 -v "$PWD:/src" -w /src golang:1.27.3 bash conformance/run.sh` | bg_48 / artifact 311；96.66 s；`Full platform acceptance passed.` |
| Linux amd64 | Docker amd64 仿真；同上改为 `--platform linux/amd64` | bg_49 / artifact 312；189.80 s；`Full platform acceptance passed.` |
| Windows amd64 | 用户提供 Win11 ARM64 VM 的 x64 仿真；进程级 `GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOFLAGS=-p=2`，执行 `conformance/run.ps1` | VMPal job `ED887E75-FA97-4869-BC88-5D4D2363611C` completed/exitCode 0/stderr 空；`Full Windows acceptance passed.`；日志 `%TEMP%/cordisgo-final-20261026/windows-amd64-delivered.log` |

矩阵源码按相对路径排序，包含 `.go/.json/.sh/.ps1/.py/.go.txt` 和 `go.mod/go.sum`，排除 `.git`、生成 `.exe`，共 165 文件；逐文件 SHA-256 的 `<hash>  <path>\n` 清单再散列为 `8fa87b08b4f3e3c063de3fbe031827d4966bdd0c1d83f2d12482716d2635e70b`。Windows 逐文件核对通过；其中 worker 常量曾仅缺 gofmt 空格，已格式化匹配。矩阵后仅对 CLI/store 两文件执行 gofmt，无语义变化，交付清单摘要为 `41674791b2b6df4e6ffe9e0734c36204e157508c7809685016ebe063f4d9ad26`；格式化后 packagehost/原始 Adapter 的 integration/race 通过，CLI 重新构建，使用新构建 native/WASM counter 分别实际 pack → inspect → 显式 dev run，均返回 `{"value":12,"events":0}`。完整矩阵没有因纯格式化重复执行。

最终交付格式的 Windows 逐文件核对也已完成：VMPal job `1B562F9D-25B1-4DDB-9460-505EC7D68B78` 退出 0，输出 `Exact formatted source identity verified: 41674791b2b6df4e6ffe9e0734c36204e157508c7809685016ebe063f4d9ad26; files=165`。

早期失败诊断运行的 Windows PID 6692 在 CIM 中仍可见，Get-Process 报 HasExited=True，终止尝试 AccessDenied；未声称该历史进程对象已彻底回收，也未为此重启用户 VM。最终验收通过受管树停止、超时和卸载检查；该历史观察不归作最终运行产生的新残留。

### 五项 capability 的场景映射

| Capability / Requirement | 行为证据与覆盖场景 |
|---|---|
| composition：定义与实例独立 | `internal/core/engine_test.go` 的 SameDefinitionInstancesOwnIndependentConfiguration；真实 basic-host 两实例独立且停一留一 |
| composition：依赖驱动激活 | DependencyReadinessAndCanceledWaiting；真实 chain 先 C/B 后 A、只初始化一次；等待取消后 provider 到达不唤醒 |
| composition：provider 退役顺序 | ProviderRetirementCancelsBeforeConsumerCleanup、TransitiveCleanupAndIndependentInstances、CleanupServiceFailureRetainsAllDisposers；真实 C→B→A release/shutdown 确认 |
| composition：重新绑定 | BindingReplacementCannotCommitOldInitialization；真实 Replace 产生新 activation，旧身份不能覆盖 |
| composition：隔离作用域与局部覆盖 | RealmIsolationAndParentOwnership；basic-host 独立 realm，chain 父子/optional 等待及 builtin child 替换/停用 |
| composition：effect 所有权与失败清理 | FailureAggregatesCleanupAndExplicitRetry、`contract/group_test.go` 初始化失败/错误/panic 仍清其他资源、并发 Close 共享结果 |
| composition：可预测的清理依赖 | GroupCloseConcurrentSharesReverseSerialRelease、GroupCloseCanceledWaiterDoesNotReleaseOrCancelCleanup；真实传递 release 次序 |
| composition：配置校验与版本状态 | ConfigurationSupersedesLateStartAndRejectsInvalid；schema 字段校验；无效更新保留 active/config/revision |
| composition：配置更新收敛 | 同上可控 1→2→3、IncompatibleMinorWaitAndCanceledUpdateWait、ConfigurationCommitCannotRacePastStopOrClose；真实停止后 Update 不落盘、重开 Restore 保留配置 |
| composition：类型化分发契约 | EventDispatchHandledZeroAndParallelErrors、MiddlewareRejectsSecondNext、TypedEventStrictDecodeAndFirstHandledZero；真实外部 First 保留 0 |
| composition：订阅与并发所有权 | InstanceCallbacksRemainSerialAndStopRejectsQueue；有界并行等待全部，Stop 取消在途并拒绝排队 |
| packages：外部包独立加载 | PackDeterministicAndSnapshotImmutable 实际目录导入；basic-host/chain 真实签名 ZIP 安装/启用/停用/重启而宿主不重编译 |
| packages：Manifest 与产物选择 | ValidateManifest、StoreRejectsSigningKeyBindingAndConfigBeforeExecution、协议协商回归；平台/主版本不符在执行前拒绝 |
| packages：可追溯身份与版本选择 | StoreTrustTamperAndConflict、StoreInstallRecoverySelectionAndData；同 ID/version 不同 digest 拒绝、已安装和已选择分别恢复 |
| packages：包路径与资源限制 | ExtractZIP 无效夹具拒绝、实际读取配额与恢复、ImportDirectory 链接拒绝；根外路径/别名/膨胀/损坏/取消，失败后正常包仍可安装 |
| packages：完整性与信任 | IndependentSigningInteroperabilityVector、StoreTrustTamperAndConflict、StorePublisherAuthorizationAndDevDigest；篡改、吊销、另一可信发布者使用同 ID、精确 dev digest |
| packages：安装提交与取消 | StoreRecoveryProcessExit 的五个安装边界、StoreRecoveryCancellationBoundary；只安装 v2 不切换 v1，未提交取消不增加安装，已提交不伪报取消 |
| packages：包依赖预检 | StoreDependenciesDataVersionAndLock、Manifest/范围回归、真实 optional Status 诊断；缺依赖/循环不同于服务等待 |
| packages：初始化与发布 | FailedPublicationRollsBackPreparedOwnership、PublicationHooksShareActiveGate、SDK failed-start 清理 provenance；真实失败激活无服务且可同 ID 恢复 |
| packages：可恢复的代码更新 | 真实 failed-new-code-restores-old-version-live；StoreDependenciesDataVersionAndLock 反向依赖；CommittedMigrationSurvivesFailedSelection 保留已提交数据事实并拒绝旧代码 |
| packages：停用与卸载 | basic-host/chain 正常 Disable/Uninstall；Store 依赖阻塞、数据保留/显式删除；Windows 真实持句柄 pending/reopen |
| packages：中断恢复 | StoreRecoveryProcessExit 的四个选择边界、CatalogIsOnlyCommitPoint；真实子进程 os.Exit(86)、锁重取、残留清理与新 activation Restore |
| execution：外部执行形式 | 独立 counter/chain module 构建 native/WASI；真实签名包公开 Host 调用两种产物，未用静态注册代替 |
| execution：会话协商 | ProtocolSelectsHighestCommonMinorAndRejectsRequiredUnknownFeatures；不兼容 major/range/required feature 拒绝，不发布注册 |
| execution：有界且可验证的消息 | `wire/{frame,session,backpressure}_test.go`；字段存在性、非法 UTF-8、方向/未知关联、洪泛和不读响应，健康邻会话继续 |
| execution：可取消调用 | EndpointCancellationDiscardsLateResult、AnsweredCancellationDoesNotExpireHealthyEndpoint；真实 runtime loop/block 与 chain task.finish 控制推进 |
| execution：WASM 资源隔离 | 真实 fault guest/原始 ABI probe 检查文件/环境/凭据不可见；无 preopen/socket/process imports，loop deadline/worker kill |
| execution：原生进程信任说明 | fault native panic/意外退出只失败当前 activation，邻会话可用；公开 runtimes 文档明确同用户 trusted 权限边界 |
| execution：类型化远端服务 | `schema/generate_integration_test.go` 临时独立 module 编译并互调；CheckDataType 拒绝进程本地值，真实 SDK 泛型服务与范围绑定 |
| execution：调用重入可诊断 | WaitGraphRejectsCrossRootCycle、SDK/真实 pipe 双向与 parent 校验；排队前拒绝同链和独立根循环，取消移除等待边 |
| execution：事件交付语义 | core/SDK 分发与原始 Session 事件确认回归；真实外部 First，未确认退出返回失败，不自动重放业务 |
| execution：执行失败与重试控制 | ExplicitSupervisorSpendsFiniteBudget；真实一次重启预算、再次崩溃停止、exit-zero 失败且旧身份撤权 |
| execution：Go SDK 与独立开发验证 | 外部 counter/chain module、真实 WASI SDK call→host→response、取消/cleanup、CLI 工作流；作者不导入 host internal |
| access：宿主调用默认拒绝 | BrokerDefaultsDenyAndPreservesHostFailure、PolicyScopeRejectsWithoutExecutingAndRetainsHostCause；真实 capability 拒绝且 handler 不执行 |
| access：权限可撤销 | BrokerRevocationCancelsAdmittedAndRejectsNewCalls、RevocationDuringScopeCheckClosesAdmission、GrantRevocationDoesNotRevokeOtherCapabilities |
| access：受控凭据与数据投影 | fixturehost Document 的 secret/path 不序列化，真实 guest probe；broker 仅返回批准投影，不传认证对象 |
| access：activation 撤销与迟到写入 | ContributionOldActivationCannotRegisterOrCommit、task 取消/转移终态回归；真实 crash/监督后旧身份拒绝 |
| access：贡献批量可见性 | ContributionBatchInvalidIsEntirelyInvisible、PublicationGateIsBatchWide；真实原始 ABI 一项非法全批不可见 |
| access：任务生命周期 | TaskTerminalTransitionIsUniqueAndRevokeInterrupts、TaskTransferSurvivesActivationAndCallerCancellation；真实显式接管任务跨 guest 崩溃存活 |
| access：数据命名空间 | NamespaceRejectsCrossPluginAndQuotaWithoutMutation、NamespaceDurabilityAndHostConfirmedMigration、MigrationRejectsConcurrentWrite |
| access：业务事务归属 | 真实 host-transferred-task-survives；保留宿主 Task handle/独立 context，不宣称自动数据库或文件事务回滚 |
| access：结构化诊断与可观察状态 | DiagnosticDefaultsNeverEchoSecretsOrPayload、DiagnosticRedactionIsBoundedAndFailuresPropagate；runtime stderr 限额且原文不回显 |
| host-integration：独立消费、通用宿主验收 | 独立 go.mod/MIT/依赖许可证、外部作者与 generated consumer、真实 basic-host/chain 两种运行时；根 module 无框架依赖 |
| host-integration：宿主协议、隔离、贡献事务 | 当前 Adapter 审查与真实包/worker 记录见上述最终平台证据；真实 UI、游戏与领域事务由相邻 change 验收 |
| host-integration：兼容来源与覆盖报告 | VerifySources 已实际拒绝并行改动的 design 摘要；回归验证 missing/drift，只有审查并实现差异后才更新快照；MVP 不在此冒报完成 |
| host-integration：跨平台发布验证 | 上述五目标完整运行、Win11 x64 仿真和持句柄回归；记录命令、来源摘要、输出与硬件/仿真限制 |

### 互操作、工具与有界 fuzz

已执行外部 Python `jsonschema.Draft202012Validator`：4 个 metaschema、58 个结构向量、1 个独立 UTF-8 签名字节/hash 向量通过。脚本明确没有执行 17 个 codeVectors 或 Ed25519；实际 Go `TestIndependentSigningInteroperabilityVector` 验证 120 字节输入、digest `eeb65c9b74177adf880e2366d23d6a0482516710470b9deb49d67fa10b14ce8b`、RFC8032 测试公钥/签名、信任和篡改拒绝。密钥为公开测试向量。

CLI 实际构建并对 native/WASM 分别执行 pack → keygen → sign → validate → inspect；私钥不回显。保留 unsigned 副本以 `run -dev-unsigned` 精确摘要许可执行，结果均为 `{"value":12,"events":0}`。缺显式许可退出 2，未知命令退出 2；已签但未授权 key 仍退出 1，不用 dev 许可绕过坏信任。签名执行由真实宿主 TrustPolicy 链证明，CLI run 是本地开发入口。

已执行 `go test -run '^$' -fuzz '^FuzzFrame$' -fuzztime=3s ./wire`、原始 codec 的 `FuzzDecodeEnvelope`，以及 `go test -tags=integration -run '^$' -fuzz '^FuzzExtractZIP$' -fuzztime=3s ./packagehost`。一次后期样本分别 307,360 / 231,279 / 2,438 次执行通过；更早更长样本保留为历史。尝试不存在的 FuzzEnvelope 只输出 no fuzz tests to fuzz，不计入证据。有限 fuzz 无法证明不存在所有非规范输入。

当前 12 方法 Adapter 完成后另执行 `go test -run '^$' -fuzz '^FuzzDecodeEnvelope$' -fuzztime=3s -parallel=2 ./conformance/vortexgo`：69,485 次执行通过；随后 `go vet ./...`、`go mod verify` 通过。来源检查逐一确认五个已审查文档摘要，当前宿主 design 为 `3419ec2c08f05dc4fe90c2aa8352e22fcc64d204b364a3e28b6594347b2b5dc8`。真实原始 WASI smoke 已通过原 13 项及新增 `real-maintenance-pure-plan`；签名正常 Host 链实际通过无 gateway 的精确 provider 绑定、两个同 contract provider、命令共存、越界输入路由前拒绝及完整大快照/分页。

### 性能证据

darwin/arm64、Apple M5 Pro、Go 1.27.3、GOMAXPROCS=18；正常多任务负载，没有固定供电/温度/独占 CPU，不能推导普适 p95/p99。当前通用 wire 修订执行 `go test -run '^$' -bench '^(BenchmarkServiceDispatch|BenchmarkEndpointNetPipe)$' -benchmem -benchtime=1000x -count=1 ./internal/core ./wire`：

| 路径 | 32 B ns/op | 1 KiB ns/op | B/op（32 B / 1 KiB） | allocs/op（32 B / 1 KiB） |
|---|---:|---:|---:|---:|
| core dispatch | 407.4 | 351.6 | 416 / 416 | 8 / 8 |
| NDJSON net.Pipe | 20023 | 26436 | 19293 / 40342 | 161 / 175 |

真实 `conformance/cmd/measure` 使用新空的私有持久 cache 目录、3 次新 activation、每次 100 次加法，结果逐次校验。native 初始化 6.446 / 6.916 / 6.364 ms，steady 54.883 / 56.790 / 60.092 µs；WASM 按执行顺序初始化 849.911 / 44.567 / 45.984 ms，steady 708.809 / 759.813 / 872.534 µs。steady 宿主约 10.3–10.8 KiB、97.5–98.7 次分配；排除子进程/worker/guest 堆。worker SHA-256 `afc12451b2d8d1772cf27f1c3d7b6d43a9a60f83e5a49b4183e0c4af6c375abc`；native `78c755cea519c319c31ce3fc52312580f57fa41bc99f5b5e6ef2a5d398fc32da`；WASM `8abfc3e31d1b0ec9c782f147a2314cd95a32d646fca6e490214b21d7348e331a`。缓存无逐次命中探针；first/reused 仅表示启动顺序。net.Pipe 不是 OS IPC，真实 counter 负载不是游戏业务基准，不设数字门禁。

### 独立审查与已修复问题

2026-10-27 宿主接入发现的反向请求 deadline 修正：唯一 VortexGo Session 按单次 `ctx.Err()` 观察区分 timeout/canceled。`TestGuestRequestDeadlineReturnsTimeoutWithoutRetiringSession` 在旧逻辑下实际失败，修复后 race 100次通过；独立 Review 发现并复现首版两次读取 Err 的竞态，已收敛为 switch，修正后未再次独立复审。根 App 的独立签名 WASI 作者 fixture 经真实 worker 发出100ms请求、宿主等待期限后返回，观察 timeout 且同 activation 后续调用成功。darwin arm64 的 `go test -tags=integration -race -count=1 -timeout 180s ./...` 与 vet 通过；Windows 11 ARM64 普通用户实际通过 Session 100次及签名插件回归的 ARM64/amd64兼容进程，具体边界见相邻平台任务的宿主入口记录。不改变此前各目标的历史证据，也不冒报真实游戏闭环。

非作者 review 已实际提出并关闭：响应背压/内存无界、failed Start 误用 initialize parent 清理、持久父子环、历史失败阻挡移除恢复、外部 First 零值丢失、公开 typed event/middleware 缺口、非法 UTF-8 替换、builtin child Replace/Disable 遗漏、配置 durable commit 与 Stop/Remove/Close 交错、shutdown 匹配/发布及进程退出丢确认、Windows poll 长度溢出/重叠。通用生命周期和 Windows errno/私有副本窄复核无剩余 finding。当前 ABI 由另两位非作者 reviewer 分别复核维护隔离/分页与服务绑定/授权；发现逻辑服务长度和贡献连续点限制后已修复，真实签名边界回归及原始包链通过，复核均为 findings: []。Review 为静态确认，运行证据由集成者提供；不冒称独立 reviewer 运行了检查。

恢复测试模拟进程退出，不代表断电/文件系统掉盘认证。原生插件拥有用户权限，非 OS sandbox；不合作的进程内 Go callback 不能强杀。框架不实现实际 MyGo UI、游戏部署事务、完整文件/网络代理、发行密钥服务或稳定发布认证。本轮不创建远端、发布或自动 archive。
## 11. Integrator：独立拆仓与公开发布

- [x] 11.1 迁移框架、唯一 change、固定上游参考及通用 skills；VortexGo 专属 Adapter/fixture/docs 留回宿主，保留许可证和 timeout 回归。
- [x] 11.2 更改 module/import、工具名称与文档导航；独立运行测试、race、native/WASI 生命周期，复核依赖与公开内容。
- [x] 11.3 使用 gh 发布 public `Cyenoch/cordis`，从干净目录下载固定版本；VortexGo 消费公开版本并通过原始 ABI/签名作者回归，不提交宿主既有工作。

### 迁出后验证（2026-10-27）

本轮实际环境为 darwin/arm64，Go 1.27.3。独立仓库运行 `bash conformance/run.sh` 成功：
integration/race、签名 counter 的 native/WASI 双实例、真实 C→B→A 生命周期、失败初始化/退出/恢复全部通过；
`go vet ./...`、`go vet -tags=integration ./...`、`go mod verify` 与 `go mod tidy -diff` 通过。
生成器名称更新后另执行 schema/SDK integration/race，独立生成 consumer 编译与调用成功。
公开文件扫描未发现真实私钥、账号 token 或本机绝对路径；测试签名种子仅为既有固定互操作向量。
107 个通用技能文件按来源复制，Go 与文档 skills 的上游 MIT 文本随仓库保留。

VortexGo 宿主验收已分离：`bash conformance/run.sh` 完成原始 ABI 的 14 项 checks、真实签名包与跨 provider/service 调用；
Adapter 的 timeout 单次 `ctx.Err()` 回归以 race 运行 100 次通过。宿主全仓 race、vet 与依赖 verify/tidy-diff 亦通过。
平台 design 与 capability spec 的既有来源摘要 drift 仍显式报错，未重新计算摘要掩盖未逐项复核的差异；
移出框架 spec 后来源清单仅拥有宿主本地的四份文档，框架原摘要留在宿主说明中。
本次未重新运行 Windows/Linux 或 macOS amd64，不将历史平台表提升为当前修订认证；MVP 仍为 11/59。

另将待发布 Git index 导出到 `/tmp` 的隔离目录，不初始化参考 submodule、没有相邻 VortexGo，
以 `GOWORK=off go test -tags=integration -race -count=1 -timeout 5m ./...` 实际通过。
193 个原始源码/规格文件逐一核对迁移后落点，无遗漏；107 个技能文件与原来源逐字节一致。

最终非作者审查逐一核对迁移落点、技能副本、参考 gitlink、隔离导出和 Go module ZIP 消费，
未发现新的路径、业务或反向依赖缺陷；仅发现 OpenSpec skills 缺完整 MIT 文本，
已从固定安装的 OpenSpec 1.15.0 补入 `licenses/skills-openspec.txt`，并在 NOTICE 保留版权。
审查者另实际运行作者/签名包、timeout 与来源检查的 race 回归；
此审查不包括并行 MVP 缓存/会话代码，也未声称其他平台或真实 GitHub 下载已验证。

### 公开发布与外部消费（2026-10-27）

`gh repo create Cyenoch/cordis --public --source=. --remote=origin --push` 实际成功。
GitHub API 确认仓库公开、默认分支 main、MIT；首个代码提交为
`e5e1f31da5f6a3097d850b28cf954a539a31c12c`，远端 main 与其一致。
公开 Go 版本为 `v0.0.0-20261027032746-e5e1f31da5f6`，不是稳定版本标签。

从 `/tmp` 的独立 module 和空 GOMODCACHE，清空 GOPRIVATE/GONOPROXY/GONOSUMDB，
通过公开 Go proxy/sumdb 解析、下载上述版本。两个工具按 `go install ...@版本` 安装成功；
独立消费者不使用 replace，真实 Open/Close 生命周期 race 测试成功。
另以不带 credential helper 的 HTTPS fresh clone 取得相同提交，未初始化 submodule，
`GOWORK=off go test -race -count=1 ./...` 实际通过。

VortexGo 已固定上述版本并移除临时 sibling replace；其 module 解析目录位于 Go module cache。
再次执行 `bash conformance/run.sh` 成功，涵盖迁回 Adapter/fixture/API/Host 的 integration/race、
14 项原始 ABI checks 和真实签名 provider/service 链。插件 integration vet、mod verify/tidy-diff 成功。
VortexGo 的既有工作未提交、未推送；并行 App 会话/缓存增量不计入此次公开版本验证。

## 12. 审计整改与完整 DX（2026-10-27）

本组为新验收，不复用前文历史完成声明。当前未上线，用户明确不兼容旧插件；同步修改统一契约，不提供兼容胶水。

- [x] 12.1 修复取消监督淘汰、已取消 parent 在途请求、可取消发送准入；真实双向 pipe 与背压回归。
- [x] 12.2 拒绝动态所有权/服务环；统一 builtin 配置与可取消 Host 控制，验证父包替换及阻塞 Close。
- [x] 12.3 修复 durable enabled 预检、Manifest 深复制、重签刷新、提交前取消、FIFO/ZIP 元数据限额、CommitError 内存一致性。
- [x] 12.4 统一 typed null/数组/嵌入/安全整数/Nullable 严格边界，生成 number 无损往返；独立生成 consumer 回归。
- [x] 12.5 修复 SDK Parallel 聚合，补齐公开作者子实例和 builtin/external 父子组合，native/WASI 真实可选子功能验收。
- [x] 12.6 提供候选发现、显式反向依赖级联计划/执行、持久 interrupted 运行记录与有界历史。
- [x] 12.7 实现终态接管任务 Release、增量 namespace 持久化和不持全局权限锁的提交；并发撤权/故障恢复/写放大回归。
- [x] 12.8 补齐 CLI definition/服务版本、公开接入文档、typed 性能基准和跨平台 CI，所有示例不依赖 internal。
- [x] 12.9 非作者审查公开契约与关键边界；完整 race/conformance、vet、互操作、规格和有界 fuzz，记录真实平台及限制。

### 本轮实际验证与审查证据

本节只记录本轮 darwin/arm64、Apple M5 Pro、Go 1.27.3 的运行结果，不继承前文其他平台或宿主历史证据。包存储子任务已以 `a5761d0` 集成，其余整改保留在工作区；本轮未推送、发布或自动 archive，不纳入并行课程和文档站工作的完成状态。

- 最终 `go test -tags=integration -race -count=1 -timeout=90s ./...` 全部通过；`bash conformance/run.sh` 通过真实独立 counter 与 chain 的 native/WASI 构建、签名包安装、双实例、反向调用、事件、配置替换、恢复和失败路径。新增作者 optional-parent 在父发布后创建的子定义可等候迟到 provider，父停用后资源归零，子运行记录有终态。CLI integration 实际构建独立 native 插件并打包，默认选择已声明的 minor 2 后调用成功。
- `go vet ./...`、`go vet -tags=integration ./...`、`go mod verify`、`go mod tidy -diff` 通过，module 无改动；`git diff --check` 通过，固定 third_party/cordis 未修改。OpenSpec 严格校验通过。
- 外部 Python jsonschema 4.26.0 验证 4 个 metaschema、58 个结构向量、1 个固定签名字节/hash 向量；脚本明确未执行的 17 个 codeVectors 与 Ed25519 不计为 Python 结果，由真实 Go codec/package 测试另行验证。v1 schema 标识与原签名字节 fixture 保持不变，结构能力枚举同步支持 children。
- 有界 fuzz 各请求 3 秒、4 workers：Frame 333,865 次、Manifest 80,545 次、PackagePath 57,158 次、VersionInRange 135,919 次、带 integration tag 的 ExtractZIP 3,634 次执行通过。首次漏 tag 的 ZIP 命令只报告 no fuzz tests，不计为证据。有限 fuzz 不证明输入空间已穷尽。
- Windows amd64、Linux amd64/arm64、macOS amd64 的 `go build ./...` 和十个包的 integration 测试二进制交叉编译通过；没有在这些目标执行。跨平台 GitHub Actions 已配置，远端未运行，不等同于平台认证。

非作者分别复核 wire/typed、access/task/namespace、packagehost/child/cascade/host。审查提出并已修正：已确认取消仍保留监督计时器、匿名嵌入注册不一致、浮点目标接受不安全整数、容器隐式 null、Nullable 内部类型/有效值遗漏、字面 JSON 字段名误判；大配额无法重开、已释放任务 ID 被旧完成消息串写、非法 UTF-8 键有损持久化、维护排队取消仍提交；Update 等待边跨 activation 丢失、未启用 runtime 的级联提前清理、未 Restore 子记录遗漏、静态与作者子实例并发抢占 ID、已卸载的 disabled 子记录阻塞父操作。全部有聚焦回归；最后两项由非作者以作者测试及独立 overlay 在 race 下各重复 10 次确认关闭，并补测 builtin 登记冲突和失败释放预留。

Host Enable/Update/Disable 的提交后清理失败由独立 overlay 注入现有 Store commitObserver，三条 `TestReview*CommitErrorKeepsMemory` 在 integration/race 下通过：报告 CommitError 的同时同步已提交内存目标。该适配器仅存在临时审查目录，不以 unsafe/反射增加生产接口；它模拟文件清理失败，不是断电、掉盘或任意 I/O 故障认证。

namespace 回放另发现每次 write/delete 扫描全部 key 的平方级成本。修正为增量配额计数、仅 migration 扫描替换集；真实重开覆盖写、删除和额度边界回归与 access/race 通过。非作者定向复核该修改无新 finding。同机 `BenchmarkNamespaceReplay`、`-benchtime=3x`：1,000/10,000 条从约 2.78/313.60 ms 改为 0.071/0.740 ms，分配不变；只测已解码记录，不含磁盘、JSON 或 checksum，不推导完整启动加速。

公开 typed 往返 benchmark 正常非 race、每组 3 次：32 B 为 2,992/3,072/3,002 ns，4,586 B、73 allocs；1 KiB 为 13,299/13,566/13,262 ns，约 36,786–36,791 B、109 allocs。背景负载未独占，无旧版同方法基线，不声称 typed 加速或普适尾延迟。原 raw 分发基线不再代替 typed 性能。

本轮直接调整 AddBuiltin(context)、ChildScope/ChildInstance、显式级联动作、任务 token 与严格 typed/number 表示，仓内 SDK、CLI、示例、生成 consumer、文档和 fixture 同步；不提供旧插件兼容层。现有 catalog 可读取，新增持久字段写入后不承诺旧二进制降级；namespace 日志新格式不迁就旧未上线快照。

验收范围不包含真实业务宿主/游戏/UI、远端 CI 实际执行、其他 OS 运行、断电认证或不合作进程内 Go callback 的强制终止。原生插件仍有用户权限，资源释放不是事务回滚。定向审查无剩余已确认 finding 不代表所有未知 bug 已被证明不存在。

## 13. Integrator：中文文档站与 GitHub Pages

- [x] 13.1 增补文档站规格、现代 Astro/Starlight 选择与部署边界；保持已有 API、schema、签名字节和并行工作，按用户要求记录 AGENTS 协作原则。
- [x] 13.2 使用现有 docs 正文建立 Astro/Starlight 导航、中文本地搜索、移动阅读与定制主题，补齐快速开始、核心概念、作者、宿主及 CLI 指南；更新 README 和贡献入口。
- [x] 13.3 提交锁定依赖的 Pages 工作流，区分 PR 构建与 main 自动部署，校验生产子路径、内部链接及静态资源；记录首次 Pages 设置与发布前提。
- [x] 13.4 实际执行文档构建、入门命令及通用验收、OpenSpec 校验，进行独立审查，记录当前平台证据与未验证的远端范围。

### 文档站实际验证（2026-10-27）

本组在 darwin/arm64、Node 24.22.1、Bun 1.4.4、Go 1.27.3 实际执行，不把其他并行任务或历史平台结果当作本次站点证据。

- `bun install --frozen-lockfile`、`bun run docs:test`（2 项）、`bun run docs:build`、`actionlint .github/workflows/docs.yml`、`bun run spec:validate` 和 `git diff --check` 通过。最终生产构建无警告，生成 19 页与 77 个文件；检查器逐页验证内部链接、锚点与 `/cordis/` 静态资源。另导出 package/lock/docs/site/scripts 到不含 node_modules、.astro、dist 的临时目录，重新 frozen install、测试及生产构建全部通过，不依赖本机历史缓存。
- 从指南提取实际 Manifest 与完整 Go 示例，在临时目录构建 Native 与 WASM counter、pack/inspect/run，均实际返回 `{"value":12,"events":0}`。独立作者与宿主 module 固定公开版本 `v0.0.0-20261027032746-e5e1f31da5f6` 编译通过，作者 Native/WASM 都成功；另按文档设置指向当前检出的绝对 replace，`go doc contract.CreateChild` 与作者构建通过，再移除 replace。早期 smoke 使用的是临时 worker 绝对路径，不能作为初稿相对路径示例正确的证据；独立审查发现后已统一修正指南、CLI 与宿主说明。
- `bash conformance/run.sh` 实际通过当前源码的 integration/race、签名 Native/WASM 双实例与两条完整生命周期链；`go vet ./...`、`go mod verify` 通过。此处只报告本机 macOS arm64 运行，没有新增 Windows/Linux 实际执行结论。
- 使用本机 Chrome / Playwright 检查 1440px 桌面和 390px 手机：浅色首页、指南、深色、中文搜索弹层、移动菜单与目录跳转；页面 scrollWidth 均不超 viewport，无 JS 错误或失败资源请求。Pagefind 的“插件”返回 15 页、“Scope”返回 10 页，首条 URL 均保留 `/cordis/`；代码复制读回剪贴板内容正确，无法匹配的搜索显示中文空态，Escape 关闭弹层。中文代码复制反馈与框架提示已核对，静态设计 detector 无确定性 finding。截图为被忽略的本地验收资产，不提交。
- GitHub API 已实际创建 Pages 设置，回读 `build_type: workflow`、目标 `https://cyenoch.github.io/cordis/`。本轮未提交、推送或触发远端工作流，因此没有声称已上线；首次远端部署和 Ubuntu Actions 环境仍须在源码推送 main 后观察结果。

首轮非作者审查指出并已修正：worker 相对路径不可执行；已公开版本不含新的子实例 API；仅 deploy job 的锁可能让较旧构建覆盖较新站点。修正为绝对路径示例、明确已发布/本地开发版本及 replace 步骤、workflow 级 ref 并发串行。

最终非作者视觉/交付审查实际阅读全部七张桌面、手机、深色、搜索和菜单截图及设计/实现，抽查主要文字对比度、H1 与可访问名称；未发现材料级视觉或任务可达性问题，确认首轮三项修复，但 disposition 为 `fix`：作者指南在创建源码之前运行 tidy 会移除 requirement。集成者将 tidy 移至保存 main.go 之后、构建之前；分别从两个空目录实际执行文档中的 init/get、可选绝对 replace、保存源码、tidy、Native/WASM build，两条路径都通过，当前检出路径另确认 CreateChild 可用。修正后 docs:test、19 页生产构建/链接检查、actionlint、OpenSpec 与 diff-check 再次通过。该小修复由集成者直接验证，未另称非作者已重新给出 ship。屏幕阅读器、完整键盘遍历、200% 缩放和远端 Pages 发布未实测。

## 14. 完整使用手册、中文校订与 commit 同步

- [x] 14.1 盘点公开使用范围，更新完整手册、中文校订和代码基线规格及设计，不改变框架契约。
- [x] 14.2 补齐组合、访问及生成服务的完整可运行教程、公开 API 导航、症状排障与上线维护，关联既有机制参考。
- [x] 14.3 按 humanizer-zh 校订全部中文手册正文，保留事实、条件、代码和验收边界。
- [x] 14.4 为每篇手册登记代码 commit 与关联路径，提供页尾基线和本地/CI 同步报告，验证历史、删除/新增及工作区差异。
- [x] 14.5 实际执行教程、生产构建、同步回归、通用验收及规格检查；独立审查内容与同步机制，记录真实平台和未验证范围。
- [x] 14.6 为安装命令和版本说明添加构建期变量，按代码基线和唯一工具链配置解析；验证静态输出、未知/缺失变量失败及历史数据不被改写。
- [x] 14.7 将网站维护说明移至仓库贡献文档，移除发布路由、导航与搜索入口，保留开发者需要的 API 代码基线。

### 完整手册实际验证（2026-10-27）

本组在 darwin/arm64、Go 1.27.3、Node 24.22.1、Bun 1.4.4 执行。框架代码基线为 `0fa22bc6ba8210c8c9ae24d15be5a3855686041a`；本轮不改公开 Go API、go.mod/go.sum、schema、签名字节或只读 submodule，文档与工具改动仍未提交、推送或发布。

- 站点现有 23 篇开发者文章，新增组合、访问、生成服务三篇完整实操及 API、排障、上线维护三篇指南。全部正文按 humanizer-zh 通读校订；准确的限制与历史证据保留，不以删限定词换取口语感。网站维护说明移至 `.github/DOCUMENTATION.md`，仅由 CONTRIBUTING 引用，不发布为用户页面。
- `bun run docs:test` 最终 6 项通过。真实临时 Git 仓库覆盖已提交、暂存、未暂存、新增、删除、无关路径、非祖先 commit，以及暂存改动被工作树恢复抵消的情形；真实本地 Go module proxy 覆盖基线版本解析、忽略检出 module/replace、未发布基线与缺依赖失败。Markdown 渲染测试覆盖不同页面 commit、正文/行内代码/命令替换、未知及畸形变量、标题/链接禁用和历史数值保留；生产检查拒绝文本和 HTML 属性中未展开的变量。
- `bun run docs:sync` 核对 23 篇记录；生命周期页对新增但尚未提交的 `conformance/docs_integration_test.go` 保留 1 条复核提示，不自动提升 SHA。`bun run docs:examples` 在临时独立 module 直接提取正文运行，三篇教程均以 race 通过并匹配预期输出；生成服务由当前 CLI 实际生成。
- 通过 Go module 查询确认基线公开版本为 `v0.0.0-20261027063313-0fa22bc6ba82`。从当前作者页提取完整程序，在新 module 实际 init/get/tidy，确认 CreateChild 可用并构建 Native/WASI；从宿主页提取 host.go，在另一新 module 使用构建期解析版本 init/get/tidy/build 通过。原始 Markdown 保留变量，最终静态 HTML 和代码复制内容包含精确版本；不查询或自动声称 latest。
- `bun run docs:build` 通过，最终为 24 页（含 404）、88 文件；链接、锚点与 `/cordis/` 资源全部解析。另逐篇解析产物验证 23 个单一 H1、对应 commit 页尾、已展开的安装/Go/Runtime 版本；维护路由和 sitemap 入口不存在，404 没有虚构基线。删除本轮生成的 .astro/dist 后清洁重建也通过。本轮未运行浏览器，不将历史截图、动态搜索/复制/移动端结果当作新增 UI 证据。
- `bash conformance/run.sh` 最终重跑通过，包含 integration/race、真实签名 Native/WASM 双实例、宿主回调及两条生命周期链。第一次运行在生成教程的子进程启动时超时，系统 sample 只显示 dyld 启动栈，不能据此认定框架死锁；清理本轮遗留进程后，定向教程通过、同一生成程序独立运行 20 次通过、完整入口重跑通过。保留首次失败记录，不将其写成从未失败；没有修改框架来掩盖此现象。
- `go vet -tags=integration ./...`、`go mod verify`、`go mod tidy -diff`、`bun install --frozen-lockfile`、两份 workflow 的 actionlint、OpenSpec strict 和 `git diff --check` 通过。CI Node/Bun 从唯一配置读取，构建前配置 Go，部署前检查 main HEAD；job 显式保留 contents:read 用于该查询，Pages/OIDC 写权限只在部署 job。

非作者内容审查独立执行三篇教程并对照公开 API，指出并已修正五项：事件传播实际向祖先通知；任务逻辑 ID 不能因 token 更新而重用；BeginTask/Finish 需独立、有期限的控制 context 且任务不随单次请求自动取消；外部 SDK 直接 Defer 不捕获 panic，Group 才提供该保证；默认 stderr 只限额排空，不自动转发 Sink。小修正由集成者对照实现完成，不另派 agent 为同一措辞变更盖章。

最终非作者独立审查同步工具、构建变量、关联路径及发布权限，实际运行当时 5 项测试、同步和构建，并核对 23 个基线、69 个复制数据块和 23 份 Pagefind 片段。使用空 Go 缓存及 GOPROXY=off 时构建明确失败，恢复环境后成功，未静默复用旧版本。审查指出三项：暂存与未暂存相抵时漏报；HTML 属性残留变量未拒绝；性能页遗漏 namespace_log.go、组合页遗漏 service.go。集成者先补回归并实际看到前两项失败，再并入 cached diff、检查属性值、补关联路径；最终 6 项测试、23 篇同步、24 页/88 文件构建、actionlint、规格及 diff-check 通过。另用真实页面 codePaths 建立临时仓库，分别修改两个遗漏实现文件后，对应页面均正确报告变化；未触碰真实索引或他人写集。小修复直接验证，不另称非作者已重新给出 ship。

未验证本轮其他 OS/Arch、屏幕阅读器、远端新工作流和本次未提交版本的部署。只读查询确认基线曾成功完成 Documentation run `40461804675`，不代表本轮文档增量已经上线。并行 `isolate-oversized-responses` 与 `negotiate-session-frame-budgets` 仅由其 owner 编写未来规划，本轮未改其写集，也未在手册中承诺尚未实现的行为。

收尾时并行 owner 已创建上述两个 change、尚未补完 delta，全仓 `spec:validate --all --strict` 因这两个在写规划报错；本组单独执行 `openspec validate build-go-plugin-framework --type change --strict` 通过，67/67 任务完成。未为使全仓暂时转绿而修改、跳过或删除并行 change；需由其 owner 完成规划后再验全仓。

## 15. 单源配置、生成与宿主/作者体验

本组为用户本轮要求新增的实施任务，全部从未完成开始；前文完成勾选、复现调查和其他线程执行结果均不代表本组已验收。仅本组直接修订当前未稳定契约，不提升 package/session/contract 兼容 major，不保留旧字段、旧默认回退、双轨读取或迁移 shim。签名算法与输入布局无需求变化，继续复用；原始结构向量按新契约更新，历史证据不改写。

每组在对应行为实现时同步其参考文档及示例，不推迟到最终集成：15.B 拥有 `docs/contracts.md`、`docs/generated-service.md`；15.C 拥有 `docs/composition.md`、`docs/recovery.md`、`docs/host-guide.md`；15.D 拥有 `docs/access.md`、`docs/resources.md`、`docs/plugin-author.md`、`docs/runtimes.md`。15.E 拥有 `docs/cli.md`、`docs/packages.md` 与入门/教程集成；需要修改其他组已拥有文章时由 Integrator 串行合并，不并行覆盖。`docs/protocol.md` 中预算和超限说明归两个消息 change，同一文件其他必要契约说明也须协调。

### 15.A Integrator：公共契约和所有权，其他写集的前置依赖

- [x] 15.1 对五项规格和 design 15 的公开接口、提交/取消、缺省/null、版本及无兼容策略做非作者审查；把全部问题映射至下表，确定共享 wire 工作与两个消息 change 的串行合并顺序，不另定义预算或响应替换。
- [x] 15.2 直接修订 `contract/`、`schema/` 公共模型、机器 schema 和结构向量：移除包级配置、每 definition 唯一配置、配置-only 作者声明、具名类型/本地引用、生成描述及构建指纹；沿用既有 schema 标识，严格拒绝旧未知字段，同步核心调用者，不保留 v1/v2 分派。验证两种定义独立配置、篡改摘要拒绝及原签名算法独立向量。
- [x] 15.3 在共享 EncodeData/DecodeData 修复 json.Number 词法/token、安全整数和有限性；验证引号字符串、不安全整数及非有限值拒绝，精确合法小数往返，生成/手写 typed、namespace 和业务测试共用同一规则。
- [x] 15.4 定义并同步小能力接口、realm-bound caller、结构化等待/实例清单/逐条 Restore 结果、当前授权撤销和 namespace outcome；同步当前 PluginSession/core 的 activation 发布确认与退役通知、通用控制帧及自定义适配 seam，明确迟到/重复/竞争。更新 codec/向量并检查依赖方向，不引入大 Context、新引擎、兼容 major 或旧载荷 fallback。

### 15.B 文件与生成器：`config/`、`schema/`，依赖 15.2–15.4

- [x] 15.5 实现唯一严格文件 loader 与来源位置映射，审查 TOML/YAML 依赖及许可证；统一显式格式/扩展名、数值精度、输入/深度/集合限额和原生值映射。聚焦回归重复/未知键、非法 UTF-8、尾随文档、YAML alias/merge/tag/非字符串键、TOML 日期/进制、非有限数及 null/非对象根；不轮流试解析或隐式补值。
- [x] 15.6 扩展现有 schema compiler/generator，单源生成 Config、严格 codec、typed Start、definition/service/Requirement 描述和包元数据；无服务配置合法，不增加并行 Go tag/反射 schema 路径。用独立 module 验证两配置定义和只注册贡献的插件，无手写重复 schema/Manifest 描述。
- [x] 15.7 消除独立生成文件 Caller/Registrar 重定义，支持多输入统一符号检查；普通叶子用内建/共享类型，复用 Optional/Nullable，提供填 tag 的联合构造器。独立编译两个服务同包互调，验证真实碰撞在覆盖前拒绝，缺失/null/零值及根约束保留。
- [x] 15.8 生成客户端接受含 Requirement 与本地方法契约引用/指纹的消费者描述，复用实例范围；验证 1.1 客户端、1.1–1.3 消费者与 1.2 provider 成功，超范围、错服务及同名同 major 错方法身份拒绝。身份来自同一规范描述，不强制不同 minor provider 指纹相等，不自动放宽版本或证明语义兼容。
- [x] 15.9 从同一受限契约导出标准 JSON Schema 2020-12，转换 nullable/闭合对象/union，并标注不能等价投影的数值、base64、RE2 约束；用独立标准 validator 检查导出和代表性向量，提供 JSON/YAML/Taplo 关联示例，不安装自制编辑器插件。

### 15.C Host/core：根 Host 与 `internal/core/`，依赖 15.2–15.4

- [x] 15.10 在统一预检边界按 definition 校验 Enable/Update/Replace/child 的配置；规范省略为 `{}`、保留 null，空对象必须满足完整根契约。验证 builtin/native/WASM 一致、必填/非对象根省略拒绝、多定义错配及非法更新/替换在 durable commit 与旧激活退役前失败；业务初始化失败仍遵守独立恢复语义。
- [x] 15.11 将 Resume、Replace、Stop/Disable/Close 等的意图提交与完成等待分离，复用条目/树预留及登记代次；在 race/可控调度下验证等待者不阻塞 provider/无关登记、同条目冲突、Close 取消、迟到结果和旧新不同时发布，不另建全局管理队列。
- [x] 15.12 提供当前实例查询与只读清单，结构化依赖诊断含 minor 范围、可见 provider 与失败原因；验证 Restore/Replace 后可继续管理，旧句柄明确失效、不泄露不可见 realm 内容，包 Range 错误显示实际约束。
- [x] 15.13 实现逐条可重复 Restore，匹配已有登记身份/realm/parent/definition/PackageRef，只补缺失，不自动 Retry Failed 或撤销本次临时 Stop；验证部分失败修复后续跑、无关树继续、父失败子等待、目标冲突不覆盖。
- [x] 15.14 明确 Stop/Resume 的临时状态、Disable 的 durable enabled 与 Remove 的记录语义，状态和返回结果一致；验证本进程重复 Restore 与真正重开宿主的不同结果、等待取消不回滚已提交意图、提交后错误仍同步内存。
- [x] 15.15 统一 builtin 与 SDK 的 startup/activation context，提供显式并发的受管工作和资源接管 seam；验证暂停 publish 确认时 Active/applied/等待者不得提前成功、最终发布拒绝时工作从未运行、重复/迟到 publish 不复活、通知失败可见、消费者清理前 retire 已通知取消、常驻工作不占服务执行位、自身清理先等工作退出、登记失败关闭资源。覆盖真实外部与自定义 Session 适配，不假报强杀不合作 Go。

### 15.D SDK/access/runtime：契约完成后可按不重叠文件集推进

- [x] 15.16 在 `sdk/` 复用逐项 panic 安全 LIFO 清理、一次关闭与聚合错误；提供标准进程入口和能力 helper，删除示例具体 Client 断言。验证初始化失败/取消、cleanup panic 后其他资源仍释放、信号/退出与 stdout 纯协议，保留可返回错误的 Serve。
- [x] 15.17 在 `access/` 增加受信任 owner + capability 当前授权撤销、所有终态任务显式释放；验证在途取消/其他 grant 不受影响、旧 activation 拒绝、普通任务反复创建超过旧保留限额、活跃不可删、旧句柄和 token 防重放不变，不隐式淘汰。
- [x] 15.18 在 `access/` 和 SDK 能力 helper 实现标准 typed broker/contribution 与 namespace CRUD/bytes 客户端；有效应答的 missing/denied/quota/未提交/committed/recovery 保真，可控复现提交后应答前取消、撤权、断链，客户端均返回 submission-unknown 并保留原原因、拒绝迟到结果和自动重试。验证受信任 owner、显式迁移/删除；与 15.16 SDK 写集由 Integrator 切分。
- [x] 15.19 在根 Host 接入显式可委托的默认 guest dispatcher，保留完整替换；验证自定义一个方法时能力/任务/子实例仍可用、业务错误不 fallback/不执行两次，宿主自有 ABI 不出现双 envelope。
- [x] 15.20 复用 `access.Sink` 接通 Host 与 Runtime 本地 cause/stage、opt-in 限额脱敏 stderr 和可观察 Sink 失败；验证默认秘密不回显、原错误与回调失败均可查、异步失败有界不递归，不改两个消息 change 的 wire 失败策略。
- [x] 15.21 提供公开轻量业务测试 harness，复用真实 definition/codec/生命周期，覆盖配置/服务/host-call/cleanup，无 internal、net.Pipe 或手写 initialize；明确测试 port 与真实授权、wire、native/WASI 的验证边界，不用 harness 替换既有协议回归。

### 15.E Package/CLI 与公开交付：依赖 15.B–15.D，相同文件不并行写

- [x] 15.22 `packagehost/` 复用现有 staging 接受三种作者包源及契约引用，生成显式机器 plugin.json 后计算完整性，原源文件不修改；检查候选歧义、默认/空列表、单份发行版本、provider 简写、consumer 显式范围、初始/已迁移 dataVersion，拒绝 float/patch 服务版本及旧字段 fallback。
- [x] 15.23 CLI 提供薄 `init/build` 闭环及机器构建描述，复用同一声明图生成/构建并记录实际 SDK 能力、目标及产物 hash；pack 检查声明漂移/旧生成代码/不匹配产物，外部构建须显式描述，不能执行插件探测或以 CLI 版本替代 SDK 能力。
- [x] 15.24 CLI 统一 help/version/退出码，增加原子格式化 `generate -output`、config/input 文件与唯一 stdin、默认有限且可配置的 startup timeout 和 stderr 等待诊断。实际子进程验证 help 成功、非法参数失败、旧文件不被失败生成/写入损坏、默认缺 provider 有限退出、取消及时、不混 stdout，不隐式授予 dev/native 信任。
- [x] 15.25 同步独立作者/宿主 module、Native/WASM 示例及完整教程，默认 TOML 并说明 YAML/null、JSON/工具路径；展示无需具体 Client 断言、realm 手工适配、重复版本/Manifest 或协议测试样板。保留人工代码基线与并行 docs 增量，未提交的实现不提前伪造 codeCommit。
- [x] 15.26 由非作者审查新公开契约、配置/生成/提交与生命周期安全，运行聚焦回归、独立标准 schema/签名向量、race/integration、通用 conformance、vet/mod 验证及 OpenSpec strict；按真实运行平台记录结果，其他 OS/Arch 与远端 CI 未运行明确说明。与两个消息 change 联合验收时分别报告，不重复勾选或借历史通过替代。

### 问题与验收归属

| 本轮问题 | 规格中的 Requirement | 实施/回归任务 |
|---|---|---|
| 原始 Config、重复 schema/字段、同包不同配置、校验过晚、nil EOF | composition：定义级类型化配置、配置校验与版本状态；execution：单一作者声明与配置生成 | 15.2、15.6、15.10 |
| TOML/YAML/JSON、精确数值/位置、null 与原生类型边界 | host-integration：统一的严格配置文件入口、文件格式能力诚实表达 | 15.3、15.5 |
| 编辑器误把自有 dialect 当标准、schema 补全 | host-integration：标准编辑器配置体验 | 15.9、15.25 |
| 重复版本/依赖/描述、服务 major/minor、构建能力不真实 | packages：面向作者的多格式包声明、版本职责清晰且只维护一次、元数据与实际构建一致 | 15.2、15.6、15.22、15.23 |
| 生成同包重定义、无意义命名转换、Optional/union 样板 | execution：生成代码可组合且避免无意义转换 | 15.7 |
| 生成客户端缩窄 minor、Host 不满足 Caller、realm 手工适配 | execution：类型化客户端复用显式消费者范围 | 15.4、15.8、15.12 |
| 具体 SDK 断言、重复 main/握手/Decode、缺业务测试工具 | execution：共用作者能力与可测试启动、SDK 标准进程入口 | 15.16、15.18、15.21、15.25 |
| cleanup panic、Start ctx 差异、后台发布/退役与并发边界、资源登记失败 | composition：effect 所有权与失败清理、订阅与并发所有权、一致的启动与受管工作生命周期 | 15.4、15.15、15.16 |
| json.Number 字符串及非有限/不安全数绕过 | execution：严格且可往返的数据契约 | 15.3 |
| Resume 全局锁长等、Replace 冲突、管理取消 | composition：管理提交与完成等待分离 | 15.11 |
| 恢复/替换后无句柄、不可续跑恢复、Stop 再开差异、弱等待诊断 | composition：当前实例可查找与状态可解释、可重复的恢复与逐条结果、临时停止与持久停用分别表达 | 15.12–15.14 |
| Grant 句柄丢失、普通终态任务永久占额度 | access：当前授权可由宿主定位撤销、终态任务资源可释放 | 15.17 |
| namespace 重复 JSON/bytes、committed/recovery 丢成 internal、应答丢失后提交未知 | access：标准能力代理消除重复编解码、存储代理保持提交结果语义 | 15.18 |
| HandleGuest 替换全部路由、本地原因/stderr/Sink 断开 | host-integration：通用分发可显式组合；access：诊断连接本地原因且不泄露 | 15.19、15.20 |
| CLI 帮助/版本/生成截断/等待无期限/输入文件/起步重复 | execution：安全且完整的本地开发闭环 | 15.23–15.25 |
| 误增兼容 major 或旧字段分支、示例/向量不同步 | packages：显式演进定义级包配置契约；host-integration：无兼容胶水的统一开发者交付 | 15.1、15.2、15.4、15.26 |

消息帧预算、发送前超限分类和普通响应隔离仅由 `isolate-oversized-responses` / `negotiate-session-frame-budgets` 的 tasks 拥有，不重复纳入上表。

### 本轮规划审查与验证（2026-10-27，仅规划）

本轮只增量修改本 change 的 proposal、design、tasks 和五份 capability spec，共八个既有文件；未修改实现、schema 文件、SDK、docs、ROADMAP 或两个并行 change，未提交、推送、部署或 archive。保留前文并行文档工作及其 67 项历史完成状态；新增 26 项实施任务全部未勾选，不能据此报告 DX 功能已交付。

两路非作者静态审查共提出五项并由 Integrator 修订：外部工作需等待 Host 最终发布并在消费者清理前收到退役取消；后台工作明确为不占回调执行位的主动并发；应答丢失的 submission-unknown 与存储 recovery-required 分离；消费者描述关联生成方法契约身份而非只看 Requirement 版本；省略配置得到的空对象仍须通过完整根契约。修订已同步到对应规格、设计及验收任务；小修由 Integrator 核对，未另称已由非作者复验。此前一位审查者因本线程小修暂停，未给出实质结论，不计入两路完成审查。

实际执行 `bun run spec:validate`：三个 change 严格校验均通过（3 passed / 0 failed）；`git diff --check -- openspec/changes/build-go-plugin-framework` 通过。另检查本 change 的 91 个 requirement 名称唯一、描述长度及 135 个场景 WHEN/THEN 结构、26 项新任务编号/未完成状态和本地规划链接均有效。两个并行 change 的 MODIFIED delta 仍有 archive 提示：须先将本 change 的 capability 建立为主规格，再应用其增量；本轮不自动同步或归档。

本轮未运行实现测试、conformance、性能、其他平台或新的部署；这些属于待实施任务，不借此前复现、历史通过或其他线程结果代替。

### 第 15 组实施记录（2026-10-27，持续更新）

- 15.1 非作者审查发现 1 项 P2：本地发布提交与远端确认的完成门槛不明确；已同步 composition scenario、design 15.6 和 15.15，要求确认后才 Active/applied/完成等待者。归属 15.4、15.11、15.15、15.26。其余公开边界未发现新增规划缺陷，不代表实现已验收。
- 共享写集合并顺序：消息 owner 完成 wire 隔离、预算及 Host/SDK/runtime 接缝后交接；Integrator 增量接 publish/retire，不重定义预算。config、schema、core、SDK、access、package 与 CLI 各在隔离 worktree；公共模型/schema/OpenSpec 由 Integrator 统一。
- 15.3 `contract` 数值 codec 已对 json.Number 词法、数字 token、有限性、安全整数及精确小数加入聚焦回归；包含 encoding/json 将空 Number 隐式变为 0 的输入检查。实际 Darwin/arm64：`go test -race ./contract ./access` 通过。
- 15.17 独立 worktree 实现及回归经 Integrator 读 diff 合入：受信 owner+capability 撤权、所有终态 task 释放、旧句柄和 token 防重放。实际 Darwin/arm64：`go test -race ./contract ./access` 通过。尚未代表全仓或其他平台验收。


### 第 15 组整合验收（2026-10-27，当前源码）

本节只记录本轮实现及实际执行，不改写第 1–14 组历史。直接修订 current v1；未加入 v2、旧字段别名、缺字段兼容默认或双轨 codec，签名域和输入布局未变。独立 worktree 交付已按最新磁盘内容增量合入；共享预算和响应隔离分别归其两个 change。

- 15.2–15.4：definition 唯一配置契约、Consumer 方法身份、显式 cleanup 分类、Publish/Retire 和窄能力接口同步到 API、codec、schema 及向量。`TestDefinitionSchemasRemainCoveredByPackageIntegrity` 对两个 definition 分别篡改配置 schema，Snapshot 均拒绝；独立固定 Ed25519 向量仍由 Go 实现签验，不用 Python hash 代替。
- 15.5–15.9：严格 TOML/YAML/JSON、精确数字与来源位置、单源 typed Config/Start/描述和可组合生成、显式 consumer 范围及标准编辑器 schema 已实现。非作者发现的引用放大、空 Number→0、CLI 分次指纹冲突、合法 TOML 隐式父表已修；回归包括跨 definition 共享展开预算和 CLI 两次 generate 后独立 module 编译。参考 `config/*_test.go`、`schema/references_test.go`、`schema/generate_integration_test.go`、`cmd/cordis/cli_integration_test.go`。
- 15.10–15.15：共同预检、管理提交/等待、结构化状态、Restore、Stop/Disable/Remove 与发布/退役已整合。非作者发现 cascade 未锁目标树、Update 等待锁住后续配置/Stop、取消 Close 提交、Disable 丢 CommitError、持久停用可 Resume 均修复；`management_test.go`、`cascade_test.go` 覆盖可控清理与更新竞争、真实提交后 journal 清理失败。正常 managed cancellation 不当成失败，真实 custom PluginSession 验证 ack 前 Starting/applied=0、无关操作可继续、retire 先于 consumer cleanup，typed cleanup 不包第二层 generic envelope。
- 15.16–15.21：标准 Main/Run、每项 panic-safe LIFO、受管工作、typed host/contribution/namespace、终态任务释放、显式 dispatcher、Sink 及公开 harness 已合入。`TestExternalDefinitionPreflightAndManagedLifecycle` 构建并实际执行独立 Native/WASI：两 definition 配置错配/省略/null/无效 child、Update 和 Replace 都在持久提交及旧激活退役前拒绝；常驻工作不占业务位，正常退役等工作退出后清理；真实 stderr 经过 redactor，回调失败进入可查本地状态。默认秘密保护另由 Sink 回归覆盖。custom dispatcher 回归同时保留能力、任务和 child，业务失败仅执行一次。
- 15.22–15.25：单一作者源、机器构建描述、实际 SDK 能力与产物 hash、薄 CLI init/build、原子 generate、多格式文件/stdin/有限启动、单源 counter/chain 及两种 runtime 教程已合入。自动 Go overlay 构建证明明确拒绝，外部流水线必须提供真实描述；失败不改原生成文件或 build.json。`examples/pluginmeta` 从相同 TOML 生成插件源码与宿主元数据，删除手抄版本/依赖和全零方法身份，重生成一致；docs API/作者/宿主/入门/生成/包/CLI 逐组更新。

实际平台 darwin/arm64，Go 1.27.3：

1. `bash conformance/run.sh` 于 2026-10-27T11:06:51Z–11:08:22Z 完整 exit 0，日志 `/tmp/cordis-dx-final-conformance.log`。全仓 integration/race、独立 Native/WASI 构建、签名双实例 basic-host、两条 lifecycle-smoke 均通过（各 19 项断言），包含新的真实外部 DX 回归。此前中断的预算 owner 执行不算本轮通过。
2. `go test -race . ./internal/core ./sdk -count=3 -timeout=2m`、config/schema/root 定向 race、custom/cascade/取消/配置更新定向 race 重复 3 次通过；配置/生成/包/CLI/教程组合 integration/race 通过。日志 `/tmp/cordis-dx-lifecycle-repeat.log`、`/tmp/cordis-dx-review-fixes.log`、`/tmp/cordis-dx-host-review.log`。
3. `CORDIS_SCHEMA_STANDARD_VALIDATOR=1 go test -race ./schema -run TestExportIndependentValidator -v -count=1` 使用临时 Python jsonschema 环境，5 个导出 schema、25 个数据向量、作者声明 schema/向量通过。`python conformance/validate-schemas.py` 的 4 metaschema/70 structural/1 signing bytes+SHA256 通过；其中明确未执行的 code vectors/Ed25519 由完整 Go conformance 覆盖，不能混称 Python 已验证。
4. `go mod verify`、`go vet ./...`、`git diff --check` 通过；`bun run docs:test` 6/6、`bun run docs:build` 24 页/89 文件链接和锚点、`bun run spec:validate` 4 passed/0 failed 通过。两消息 delta 的主 spec 缺失 archive INFO 保留，不自动归档或同步。

最后非作者整合复核提出的 3 项确定问题已由 Integrator 修复并通过下述最新门禁，15.26 现已完成。真实源码提交后的文档 codeCommit 刷新仍单独记录，不提前伪造 SHA。本轮未实际运行 Windows/Linux 的完整交付树或远端 CI；独立 Windows stdin change 的既有消费者证据不冒充本组全平台认证。用户已明确授权 Integrator 在全部完成后提交推送；文档基线只能指向实际包含新 API 的提交。


### 最终非作者复核修复及再次验收

最终 DX 复核复现 3 项：Restore 绕过 package/tree 预留会在替换等待期间启动旧代码；counter/chain 虽生成 execute 契约却仍注册手写原载荷协议；counter 使用 Atoi 拒绝 schema 合法的 `10.0` / `1e1`。Integrator 已直接修复：Restore 对缺失条目复用同一准入检查并逐条报告 conflict；示例仅注册生成 server、内部使用生成 client 和显式 consumer，移除重复 Request/Response，宿主/SDK/CLI/测量与 cleanup 观察载荷同步为 execute envelope，不保留原载荷 fallback；integer 按已验证的数学值读取，三种词法形态在真实 harness 中经生成 client 调用成功。

`TestRestoreCannotStartOldPackageDuringReplacement` 控制真实 Host shutdown 等待，确认 Restore 不启动旧代码，释放后 Replace 成功，重复 Restore 使用新 digest；race 重复 3 次通过。counter 独立 module race（含三个 integer 形态）、SDK/WASI、CLI 子进程、三篇教程与快速开始、pluginmeta 的 integration/race 均通过。复核者没有代替 Integrator 修改文件；上述修复及执行不冒称已另获非作者复审通过。

最新冻结实现于 2026-10-27T11:18:22Z–11:19:48Z 再次执行 `bash conformance/run.sh`，完整 exit 0；日志 `/tmp/cordis-dx-final-conformance-r2.log`。这次包含真正单源服务协议、Restore 预留回归、全部 integration/race、basic-host Native/WASI 和各 19 项的两条外部生命周期链，取代上一轮作为最终源码门禁。独立测量 smoke `iterations=1,calls=2` 的 Native/WASM 共 6 个样本通过，日志 `/tmp/cordis-dx-final-measure.log`；不以其数值设性能承诺。

最新 `go vet -tags=integration ./...`、`go mod tidy -diff`（无变化）、`go mod verify`、diff 检查及 docs:test 6/6、docs:build 24 页/89 文件通过。Windows/amd64、Linux/amd64 的 root integration/SDK/CLI 测试二进制仅交叉编译通过，产物在临时目录，不代表本组完整 Windows/Linux 执行。Windows worker 的另一非作者只读复核未发现阻塞发布的生产 bug，已提交的独立修复继续由其 change 保存实际平台和消费者证据。
