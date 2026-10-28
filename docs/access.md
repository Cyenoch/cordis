---
codeCommit: b14906720f6c0d804483253039e09a8b343be9c4
codePaths:
  - access
  - host.go
  - contract/task.go
  - sdk/task.go
  - internal/core/engine.go
---

# 宿主访问、贡献、任务与命名空间

`access` 管理宿主身份、调用准入、所有权和取消，不提供文件或网络权限沙箱。真实 I/O、范围检查、凭据代理与结果裁剪由宿主实现，Native 也不会因此获得 OS 隔离。先跑代码可从[访问实操](access-tutorial.md)开始。

## 激活与授权

使用 `cordis.Host` 时，Host 已负责激活与撤销；应用配置 Authorize、Broker 和 validator。下面的 Policy 方法供宿主访问层及协议适配作者使用，不应给同一 Host 另建身份来源。

宿主为每次启动生成全新 `contract.Identity`，用经过验证的 manifest 声明调用 `Policy.Activate(identity, manifest.Capabilities)`。同一 `(Package, Entry)` 的新激活原子撤销旧激活；已用过的 activation ID 不能再次使用。guest 请求中的身份不能代替会话绑定身份。

每个 Policy 在整个生命周期保留所有已用 activation ID，内存随该 Policy 使用过的 activation 数量线性增长；撤销不会删除防复用记录，不做静默 LRU 淘汰，也没有记录上限或裁剪开关。历史随 Host/Policy 生命周期结束、相关引用释放后回收；不能靠重建 Policy 在仍有旧会话时绕过身份隔离。

`NewBroker`、`NewContributionStore`、`NewTaskManager` 和 `NewNamespaceStore` 必须传入非 nil Policy，否则 panic；这些组件必须通过构造函数初始化，零值不可用。`OpenNamespaceStore` 遇到 nil Policy 返回 `invalid_message`。所有接受 context 的访问层 API 要求非 nil context，不需要取消时使用 `context.Background()`；已取消 context 的拒绝与提交点检查仍有效。

- `NewPolicy()`：零值也可用，实例不可复制。
- `Policy.Grant(identity, capability, scope)`：仅能给已声明能力发放宿主 handle；同能力重新授权先撤销旧 grant。
- `Grant.Revoke()`：关闭该 grant 后续调用，取消已准入调用，不影响其他 grant。
- `Policy.RevokeCapability(identity, capability)`：宿主按受信任身份定位当前 grant，复用同一撤销与取消机制，无需保存初始化时的 handle。未授予的能力返回 `not_ready`，重复撤销当前已撤销 grant 幂等；旧激活返回 `denied`，不影响新激活或其他 owner、能力。重新授权后此入口撤销新的 grant，旧 handle 不能撤销它。
- `Policy.Revoke(identity)`：关闭整次激活，撤销贡献、中断未移交任务并取消在途调用。
- `Policy.Calls(identity)`：宿主可以观察已取消但 handler 尚未返回的调用，包括已退休激活；取消不等于系统调用结束。

`Broker.Register(capability, authorize, handler)` 同时要求非 nil 的范围检查和处理器。`authorize(ctx, identity, declaration, grantedScope, input)` 必须检查声明范围与授权范围，不能信任 input 自报权限。处理器只接收数据与取消 context；宿主凭据留在处理器闭包内，不向 guest 复制连接对象、认证头、环境变量或其他插件数据。

`Broker.Call(ctx, identity, capability, input)` 每次检查声明、当前激活、当前 grant 和 scope。范围检查在锁外执行；执行完后再次检查同一个 grant，再在与撤销共用的临界区准入。撤销前已准入的处理器会收到取消；不合作的处理器仍可能产生外部副作用，但迟到结果不被返回为成功。host handler 原始错误保留错误链交给宿主；跨 guest 边界前必须用 `access.PublicError(err)` 投影，不能直接发送 `err.Error()`。

## 贡献批次与 core 发布

创建 `NewContributionStore(policy)`，为每个 slot 调用 `Register(slot, Validator)`。宿主 validator 定义 schema；框架拒绝未知 slot、重复 `(slot,id)`、缺失 ID 和非法 JSON，不自行接受执行视图或宿主对象句柄。

1. 在准备激活阶段先建立 Policy 身份。
2. `Stage(ctx, identity, contributions)` 校验整个批次并保留不可见快照；失败保留原快照不变。`ContributionError.Index` 是拒绝的数组位置，`Unwrap` 保留 host validator 原始原因。
3. core 的 BeforePublish hook 调用 `Batch.Commit()`；一次替换该 owner 的完整贡献集合。取消准备使用 `Batch.Abort()`。
4. `SetActiveGate(engine.IsActive)` 将 `Snapshot()` 的可见性绑定 core 的激活提交。gate 在 policy 锁外、每个 owner 每次快照只调用一次，避免跨模块锁反转与半批可见。
5. OnRetire 调用 `Policy.Revoke(identity)`。已暂存批次不能在退休后提交；其他 owner 的 snapshot 不受影响。

不接 core 时可以不安装 gate；此时 `Commit` 就是贡献可见点。快照返回数据副本，不暴露内部可变数据。

## 任务与显式接管

`manager := NewTaskManager(policy)`；`manager.Create(ctx, identity, id, token)` 建立 queued 任务。同 owner 的 ID 在 ledger 中仍有记录时不可重复；显式释放后可重用 ID，但调用者每次创建必须使用新的 `contract.NewID()` token。SDK 自动生成 token，begin/finish/cancel 全链携带并匹配，防止旧代次消息修改新任务；manager 不保存已释放 token 的永久历史，不能把重复使用 token 当作受支持的反重放方式。`Task.Start()` 转为 running。`Task.Finish(state, result, failure)` 提交唯一终态：completed、failed、canceled 或 interrupted；failed 必须有错误，completed 不能附带错误。完成后再次提交返回 conflict，不改终态。原始错误只通过 host handle 的 `Err()` 返回；`Status()` 仅携带稳定失败码和显式结果。

- `Task.Context()` 是实际执行工作必须遵守的取消信号；`Done()` 表示任务管理终态，不声明外部 I/O 已回滚。
- caller 取消使未移交任务 canceled；完成与接管提交前在 policy 锁内检查当前 parent 的 `Err()`，不依赖异步取消 callback 的调度。激活停止使未移交任务 interrupted，且拒绝新建任务和迟到完成。
- `Task.Transfer(hostCtx)` 是仅宿主持有 handle 才能执行的接管。只能接管仍未取消的任务；在同一临界区替换取消 parent 后，后续由 hostCtx 控制。停止插件不会杀死已接管事务。
- guest 完成消息必须走 `TaskManager.Finish(identity,id,token,state,result,failure)`；此入口拒绝旧激活，也拒绝修改已接管任务。宿主通过持有的 `Task` 完成已接管事务。
- 每个 manager 为每次激活保留至多 `MaxTasksPerActivation`（256）条记录，包含 queued、running、终态和显式接管任务；单个结果至多 `MaxTaskResultBytes`（1 MiB），累计保留结果至多 `MaxTaskResultBytesPerActivation`（8 MiB），均按原始 JSON 字节计算。超过限制返回 `ErrTaskQuota`，不新建记录、不复制结果、不提交终态；累计结果额度耗尽后仍允许无结果完成。此固定有界历史避免 guest 通过顺序完成任务耗尽宿主内存。
- 宿主可对任意普通或接管任务调用 `Task.Release()`，显式移除终态 ledger 并回收记录计数与结果额度；queued、running 返回 `conflict`，不删除活跃工作。不自动淘汰其他保留结果；同一 activation 可持续完成、释放并创建新任务。重复调用同一 handle 的 Release 幂等，旧 handle 的 Status/Err/Done 保持独立终态，迟到 Release 不会删掉复用 ID 的新任务。宿主也应释放自己的引用。
- 未保存 handle 的可信宿主可用 `TaskManager.Release(identity,id,token)`，从现有 ledger 核对完整 owner 与创建 token 后委托给 `Task.Release()`；不绕过 token，也不要求 owner 仍活跃，因此可释放退休激活的已接管终态任务。未找到返回 `not_ready`（含已释放记录），token 不符返回 `denied`，活跃任务返回 `conflict`。`Snapshot()` 保留供宿主查询现有记录，不接受 guest 自报身份作为管理授权。
- 激活撤销时 manager 删除该 owner 所有未接管记录和结果，`Snapshot()` 不再显示它们；仅显式接管记录继续保留。宿主已经持有的 `Task` handle 仍可读取终态、结果与原始 `Err()`；删除 ledger 不会破坏 handle，也不表示外部操作已回滚。

checkpoint、持久任务 journal、业务恢复和补偿由宿主负责。任务完成不表示外部事务已持久化，cleanup 也不会回滚数据库或网络副作用。

## 命名空间与配额

`NewNamespaceStore(policy)` 是明确的内存实现；需要重启持久化时使用 `OpenNamespaceStore(policy, directory)`。打开、读取、解析或持久化出错会返回错误，绝不退回内存模式。宿主目录必须由宿主独占管理；同目录只打开一个写入 store，不提供多进程并发写协调。

宿主通过 `Configure(packageID, NamespaceConfig{Name,QuotaBytes,DataVersion})` 建立一对一 package/namespace 绑定。`Read/Write/Delete(ctx, identity, namespace, key, ...)` 只允许当前激活操作其包的 namespace，不接受 guest 任意文件路径。namespace/package/key 必须是合法 UTF-8，不能由 JSON 替换字符造成键合并。配额按所有 key 与 value 的字节数合计；替换扣除旧值、删除释放额度。超额返回 `ErrQuota`，缺 key 返回 `ErrNotFound`，均不转换成默认空数据。停止插件不删除持久数据。

每次重开后可按清单重复 `Configure`：已有绑定按 `(Package, Name)` 匹配，`DataVersion` 只初始化新空间，已有数据版本保持不变，包括迁移、压缩后的版本。配额可提高或降低到不小于当前已用字节数；变化追加 `configure` 记录，重开后仍生效。配额低于已用量返回 `conflict`，不改配额、数据或持久记录；换包占用同名空间、同包换名仍返回 `conflict`。调整配额不是迁移，不会运行迁移 callback。

持久实现追加带序号和 SHA-256 校验的 `namespaces.log`，同步新增记录，不为小写入重写整个 namespace。`Compact(ctx)` 是显式维护：把当前绑定和值写到私有临时文件，同步后原子替换并同步目录；不在普通写路径自动触发全量压缩。配额 int64 在持久配置中使用十进制字符串，完整范围可恢复。单条编码记录上限 64 MiB，超过返回 ErrQuota。打开时验证每条记录的完整性、序号、绑定及配额；损坏或不完整尾部明确报错，不静默丢弃数据。

完整记录同步后失败返回 `*CommittedError`，此时数据已可见，不要当作未提交盲目重试。提交前错误会回滚文件和内存；如果文件回滚也失败，返回 `*RecoveryError`，此 store 拒绝继续操作，必须重开并处理恢复错误。相同 activation 的写入和撤权串行，但慢磁盘不持有全局权限锁，不阻塞无关 owner 的撤权。

`Migrate(ctx, namespace, targetVersion, Confirmation, Migration)` 和 `DeleteNamespace(ctx, namespace, Confirmation)` 必须安装宿主确认 callback。确认失败保留数据，迁移失败不发布，超配额也不发布。迁移在锁外执行 host callback，返回时检查版本和 revision，拒绝覆盖期间发生的并发写入。callback 是宿主决策，不是从 guest 请求解码的布尔值。

## 诊断默认不回显

`NewSink(SinkOptions{Write, Redact, MaxBytes})` 要求真实 writer；默认上限 4096 字节，最小 512。`Emit(ctx, DiagnosticInput)` 默认只产生有限身份标签、阶段、调用关联、稳定错误码和固定摘要，不复制原始 `Err`、`Payload` 或 `Summary`；普通插件日志不能作为可信状态。带路径或控制字符的身份标签会变成 `[redacted]`。

宿主可显式提供 `Redact(ctx,input)`，对自己的秘密、路径和业务字段脱敏后返回摘要。输出按 UTF-8 边界和最终 JSON 大小限制；最大标签占满预算时去掉 package、entry、digest 和 call，保留 activation 与 stage；redactor 或 writer 失败会传播，且 redactor 失败不使用原始数据 fallback。`access.PublicError(err)` 无需 Sink，只保留允许的错误 code，并将消息替换为固定安全摘要。

## 行为测试

`access/*_test.go` 覆盖撤销与准入交错、按当前能力撤权及其他 owner/grant 隔离、取消及迟到结果、整批校验失败不可见、core gate 可见性、旧激活拒绝、任务唯一终态与接管存活、取消 callback 延迟时完成/接管拒绝、任务 flood 与结果字节边界、同 activation 长期完成/释放循环、终态计数与结果额度回收、活跃任务不可删、旧 handle/token 不修改新任务、退休 ledger 清理及宿主 handle 保留、跨 namespace/配额拒绝、真实持久化/确认迁移和诊断 secret 不泄露。集成负责人统一记录单测、集成与 race 的实际结果；当前修订和平台范围以 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md) 为准。

`policy_test.go` 集中覆盖 grant/当前能力撤权的在途取消、新调用拒绝、迟到结果拒绝及范围检查期间撤权，替代 `broker_test.go` 的两项重复撤销测试；同文件另覆盖整次 `Revoke` 和同 entry 替换。命名空间测试覆盖迁移后重配、配额持久调整与拒绝、压缩重开保留版本；诊断测试覆盖最大标签和 JSON 最坏转义下的大小边界、无 Sink 投影。

## Typed namespace 与未知提交

`access.Register[I,O]` 复用 Broker 的同一授权准入及 `contract.Encode/DecodeData`。`access.RegisterNamespace(broker, store, capability, authorize)` 提供标准字节 CRUD；owner 来自受信调用绑定，namespace 按 package 绑定，guest 请求不能选 owner 或 namespace。迁移和删除整个空间仍由宿主显式确认。

`client := sdk.NewNamespaceClient(invoker, capability)` 接受 `contract.HostInvoker`；`Read/Write/Delete` 返回 typed 错误，`Do` 同时公开 `NamespaceResult.Outcome`。有效应答保留 missing、denied、quota、not-committed、committed 与 recovery-required；字节用标准 base64，线上不携带本地 cause。

请求已经发出但因取消、撤权、断链或非法/迟到应答无法确认结果时，返回 `*contract.SubmissionUnknownError` 并保留原原因，不能当作未提交或 storage recovery-required 自动重试。只有发出前已证明失败，或收到确定的存储结果，才使用对应 outcome。宿主的 `CommittedError` 和 `RecoveryError` 与共享错误类型一致，`errors.Is/As` 保留本地原因。
