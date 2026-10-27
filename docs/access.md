# 宿主访问、贡献、任务与命名空间

`access` 只管理宿主身份、准入、所有权和取消，不实现通用文件/网络权限沙箱。真实 I/O、范围匹配、凭据代理和裁剪结果由宿主提供；尤其 native 插件本身不因此获得 OS 隔离。

## 激活与授权

宿主为每次启动生成全新 `contract.Identity`，用经过验证的 manifest 声明调用 `Policy.Activate(identity, manifest.Capabilities)`。同一 `(Package, Entry)` 的新激活原子撤销旧激活；已用过的 activation ID 不能再次使用。guest 请求中的身份不能代替会话绑定身份。

- `NewPolicy()`：零值也可用，实例不可复制。
- `Policy.Grant(identity, capability, scope)`：仅能给已声明能力发放宿主 handle；同能力重新授权先撤销旧 grant。
- `Grant.Revoke()`：关闭该 grant 后续调用，取消已准入调用，不影响其他 grant。
- `Policy.Revoke(identity)`：关闭整次激活，撤销贡献、中断未移交任务并取消在途调用。
- `Policy.Calls(identity)`：宿主可以观察已取消但 handler 尚未返回的调用，包括已退休激活；取消不等于系统调用结束。

`Broker.Register(capability, authorize, handler)` 同时要求非 nil 的范围检查和处理器。`authorize(ctx, identity, declaration, grantedScope, input)` 必须检查声明范围与授权范围，不能信任 input 自报权限。处理器只接收数据与取消 context；宿主凭据留在处理器闭包内，不向 guest 复制连接对象、认证头、环境变量或其他插件数据。

`Broker.Call(ctx, identity, capability, input)` 每次检查声明、当前激活、当前 grant 和 scope。范围检查在锁外执行；执行完后再次检查同一个 grant，再在与撤销共用的临界区准入。撤销前已准入的处理器会收到取消；不合作的处理器仍可能产生外部副作用，但迟到结果不被返回为成功。host handler 原始错误保留错误链交给宿主；跨 guest 边界前必须用 `Sink.PublicError(err)` 投影，不能直接发送 `err.Error()`。

## 贡献批次与 core 发布

创建 `NewContributionStore(policy)`，为每个 slot 调用 `Register(slot, Validator)`。宿主 validator 定义 schema；框架拒绝未知 slot、重复 `(slot,id)`、缺失 ID 和非法 JSON，不自行接受执行视图或宿主对象句柄。

1. 在准备激活阶段先建立 Policy 身份。
2. `Stage(ctx, identity, contributions)` 校验整个批次并保留不可见快照；失败保留原快照不变。`ContributionError.Index` 是拒绝的数组位置，`Unwrap` 保留 host validator 原始原因。
3. core 的 BeforePublish hook 调用 `Batch.Commit()`；一次替换该 owner 的完整贡献集合。取消准备使用 `Batch.Abort()`。
4. `SetActiveGate(engine.IsActive)` 将 `Snapshot()` 的可见性绑定 core 的激活提交。gate 在 policy 锁外、每个 owner 每次快照只调用一次，避免跨模块锁反转与半批可见。
5. OnRetire 调用 `Policy.Revoke(identity)`。已暂存批次不能在退休后提交；其他 owner 的 snapshot 不受影响。

不接 core 时可以不安装 gate；此时 `Commit` 就是贡献可见点。快照返回数据副本，不暴露内部可变数据。

## 任务与显式接管

`manager := NewTaskManager(policy)`；`manager.Create(ctx, identity, id, token)` 建立 queued 任务。活跃 ledger 中同 owner 的 ID 不可重复，token 每次创建必须使用新 `contract.NewID()`；SDK 自动生成并在 begin/finish/cancel 全链携带，拒绝已释放旧代次的迟到完成/取消；`Task.Start()` 转为 running。`Task.Finish(state, result, failure)` 提交唯一终态：completed、failed、canceled 或 interrupted；failed 必须有错误，completed 不能附带错误。完成后再次提交返回 conflict，不改终态。原始错误只通过 host handle 的 `Err()` 返回；`Status()` 仅携带稳定失败码和显式结果。

- `Task.Context()` 是实际执行工作必须遵守的取消信号；`Done()` 表示任务管理终态，不声明外部 I/O 已回滚。
- caller 取消使未移交任务 canceled；完成与接管提交前在 policy 锁内检查当前 parent 的 `Err()`，不依赖异步取消 callback 的调度。激活停止使未移交任务 interrupted，且拒绝新建任务和迟到完成。
- `Task.Transfer(hostCtx)` 是仅宿主持有 handle 才能执行的接管。只能接管仍未取消的任务；在同一临界区替换取消 parent 后，后续由 hostCtx 控制。停止插件不会杀死已接管事务。
- guest 完成消息必须走 `TaskManager.Finish(identity,id,token,state,result,failure)`；此入口拒绝旧激活，也拒绝修改已接管任务。宿主通过持有的 `Task` 完成已接管事务。
- 每个 manager 为每次激活保留至多 `MaxTasksPerActivation`（256）条记录，包含 queued、running、终态和显式接管任务；单个结果至多 `MaxTaskResultBytes`（1 MiB），累计保留结果至多 `MaxTaskResultBytesPerActivation`（8 MiB），均按原始 JSON 字节计算。超过限制返回 `ErrTaskQuota`，不新建记录、不复制结果、不提交终态；累计结果额度耗尽后仍允许无结果完成。此固定有界历史避免 guest 通过顺序完成任务耗尽宿主内存。
- 激活撤销时 manager 删除该 owner 所有未接管记录和结果，`Snapshot()` 不再显示它们；仅显式接管记录继续保留。宿主已经持有的 `Task` handle 仍可读取终态、结果与原始 `Err()`；删除 ledger 不会破坏 handle，也不表示外部操作已回滚。终态接管任务由宿主调用 `Task.Release()` 从 manager ledger 和结果额度中移除；运行中拒绝释放，重复释放幂等。已有 handle 的 Status/Err 仍可读；宿主也应释放自己的引用。

checkpoint、持久任务 journal、业务恢复和补偿由宿主负责。框架不会伪造事务成功，也不宣称 cleanup 能回滚数据库或网络效果。

## 命名空间与配额

`NewNamespaceStore(policy)` 是明确的内存实现；需要重启持久化时使用 `OpenNamespaceStore(policy, directory)`。打开、读取、解析或持久化出错会返回错误，绝不退回内存模式。宿主目录必须由宿主独占管理；同目录只打开一个写入 store，不提供多进程并发写协调。

宿主通过 `Configure(packageID, NamespaceConfig{Name,QuotaBytes,DataVersion})` 建立一对一 package/namespace 绑定。`Read/Write/Delete(ctx, identity, namespace, key, ...)` 只允许当前激活操作其包的 namespace，不接受 guest 任意文件路径。namespace/package/key 必须是合法 UTF-8，不能由 JSON 替换字符造成键合并。配额按所有 key 与 value 的字节数合计；替换扣除旧值、删除释放额度。超额返回 `ErrQuota`，缺 key 返回 `ErrNotFound`，均不转换成默认空数据。停止插件不删除持久数据。

持久实现追加带序号和 SHA-256 校验的 `namespaces.log`，同步新增记录，不为小写入重写整个 namespace。`Compact(ctx)` 是显式维护：把当前绑定和值写到私有临时文件，同步后原子替换并同步目录；不在普通写路径自动触发全量压缩。配额 int64 在持久配置中使用十进制字符串，完整范围可恢复。单条编码记录上限 64 MiB，超过返回 ErrQuota。打开时验证每条记录的完整性、序号、绑定及配额；损坏或不完整尾部明确报错，不静默丢弃数据。

完整记录同步后失败返回 `*CommittedError`，此时数据已可见，不要当作未提交盲目重试。提交前错误会回滚文件和内存；如果文件回滚也失败，返回 `*RecoveryError`，此 store 拒绝继续操作，必须重开并处理恢复错误。相同 activation 的写入和撤权串行，但慢磁盘不持有全局权限锁，不阻塞无关 owner 的撤权。

`Migrate(ctx, namespace, targetVersion, Confirmation, Migration)` 和 `DeleteNamespace(ctx, namespace, Confirmation)` 必须安装宿主确认 callback。确认失败保留数据，迁移失败不发布，超配额也不发布。迁移在锁外执行 host callback，返回时检查版本和 revision，拒绝覆盖期间发生的并发写入。callback 是宿主决策，不是从 guest 请求解码的布尔值。

## 诊断默认不回显

`NewSink(SinkOptions{Write, Redact, MaxBytes})` 要求真实 writer；默认上限 4096 字节，最小 512。`Emit(ctx, DiagnosticInput)` 默认只产生有限身份标签、阶段、调用关联、稳定错误码和固定摘要，不复制原始 `Err`、`Payload` 或 `Summary`；普通插件日志不能作为可信状态。带路径或控制字符的身份标签会变成 `[redacted]`。

宿主可显式提供 `Redact(ctx,input)`，对自己的秘密、路径和业务字段脱敏后返回摘要。输出按 UTF-8 边界和最终 JSON 大小限制；redactor 或 writer 失败会传播，且 redactor 失败不使用原始数据 fallback。`PublicError` 只保留允许的错误 code，并将消息替换为固定安全摘要。

## 行为测试

`access/*_test.go` 覆盖撤销与准入交错、取消及迟到结果、整批校验失败不可见、core gate 可见性、旧激活拒绝、任务唯一终态与接管存活、取消 callback 延迟时完成/接管拒绝、任务 flood 与结果字节边界、退休 ledger 清理及宿主 handle 保留、跨 namespace/配额拒绝、真实持久化/确认迁移和诊断 secret 不泄露。集成负责人统一记录单测、集成与 race 的实际结果；当前修订和平台范围以 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md) 为准。
