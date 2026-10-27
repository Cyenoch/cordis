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
- [ ] 11.3 使用 gh 发布 public `Cyenoch/cordis`，从干净目录下载固定版本；VortexGo 消费公开版本并通过原始 ABI/签名作者回归，不提交宿主既有工作。

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
