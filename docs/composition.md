# 组合、实例与清理

`internal/core` 是宿主 Facade 的生命周期引擎，不是插件作者直接导入的公共模块。普通插件只接收 `contract.Scope`；本地强类型服务由公开 Facade 包装为原始 JSON Handler。

## 宿主入口

- `core.New()` 创建独立 Engine；`NewWithHooks(Hooks)` 集成 activation 所有的外部记录。
- `Add(entry, definition, identity)` 返回独立 `contract.Instance`；entry 必须显式设置 `Enabled: true` 才会启动。
- `ConfigureRealm(name, parent, isolatedKeys)` 声明命名空间；key 使用 `Service.Key()`。局部 provider 优先，隔离 key 不继承父服务，其他 key 可继承祖先服务。相同 realm 的同一 key 不允许重复发布。
- `AddChild(parentEntryID, entry, definition, identity)` 等价于设置 `Entry.Parent` 后 Add。父实例关闭会停止整棵子树，包括尚未激活的子实例。子实例缺失依赖或失败不会停止父实例。将可选功能拆为子实例，无需把 optional 服务混入必需依赖。
- `Call(ctx, realm, requirement, input)` 只解析已经完整发布的服务；`Emit(ctx, realm, name, input, mode)` 按显式事件模式分发。
- `Fail(entryID, err)` 将当前 activation 退役并保留故障，不会自动重试。Runtime/session 故障应使用 `FailActivation(identity, err)`，原子拒绝旧会话迟到故障影响新 activation。
- `Close(ctx)` 关闭 Engine；取消等待只表示调用者不再等待，不会将未释放资源假报为已释放。
- `Enable(ctx, entryID)` 重新启用已经完整停止的 entry；FAILED 只能 Retry。`Remove(ctx, entryID)` 等待成功清理后释放 Entry ID，再用 Add 安装新 Definition；必须先移除子 entry，失败清理不会抹掉诊断。

同一个 Engine 中 Entry ID 唯一。同一 Definition 可以以不同 Entry、多份配置运行。`Identity.Activation` 由 Engine 在每次真实启动时重新生成，调用者传入的 activation 不会复用。

## 配置与依赖快照

Definition 的 `Requires` 是当前 activation 的必需服务列表，`Provides` 是初始化必须完成的精确声明。所有注册在 Start 中暂存；Start 成功、声明齐全、无重复 provider 且 revision 仍为最新时才整体发布服务和订阅。消费者初始化前取得固定 binding snapshot，不会在调用中悄悄切换 provider。provider 退役后消费者退回等待，新的 provider 发布后生成新的消费者 activation。

`Instance.Status` 分别报告 Desired、Applied、WaitingFor 和最后错误。`Update` 先校验再增加 Desired，取消其等待不撤销有效更新。更新会停止旧 activation，最终只应用最新版本；被覆盖的等待者收到 `UpdateResult.Superseded`。旧初始化即使忽略取消并迟到完成也不能发布。失败实例只有有效 Update 或显式 Retry 才恢复。确定的服务激活依赖环被诊断为 `reentrant_call`。

Facade 的 `Status.Diagnostics` 保留包预检中可选依赖缺失/不兼容的诊断，不将其混成启动失败；必需服务未满足仍由 WaitingFor 表达。配置提交和 Stop/Remove/Close 的生命周期准入有序协调：停用之后拒绝的更新不能迟到写入 catalog，也不能复活已关闭实例。真实外部链与对应并发回归见[生命周期验收](lifecycle-acceptance.md)和[回归](regressions.md)。

## 所有权与停止顺序

一个 Scope 对应一个 activation，拥有服务、订阅、固定 bindings 以及一个逆序串行资源组。`Defer` 的 context 独立于普通工作取消；初始化失败也会执行已经登记的 disposer。disposer 逐一尝试，异常与返回错误均聚合到等待者和状态，不会因首个失败跳过其余清理。

退役先在协调锁内撤销普通准入并取消完整传递依赖闭包的普通工作；之后锁外等待消费者和子实例清理，最后释放 provider 自身资源。消费者 disposer 只能用它已经拥有的 Scope binding 调用退役中的 provider，不可通过 Engine.Call 查找退役服务或临时获取新 binding。此清理资格会沿已有绑定的同步调用链传递。没有跨实例全局 LIFO；服务依赖与父子所有权优先于登记时间。

Facade 跟踪 external 与 builtin 的同一父子所有权树。`Host.Disable(parent)` 先移除全部子实例，再移除父实例；builtin 不写入包 catalog。替换外部父包会保存并重建其 builtin 子定义，使用新的 activation。只有外部 entry 的期望配置持久化；应用重开时仍须显式注册自己的 builtin。

业务 Handler、事件回调和初始化默认占用本 activation 的串行执行槽；控制、取消、协调不占业务槽。等待图同时覆盖同步调用链及分别占用不同实例的独立根调用：形成环的新增等待边立即返回 `reentrant_call`。等待结束或取消后删除边，不自动重入。用户自行创建的裸 goroutine 不在 Engine 可等待业务集合中，作者必须以 Defer 管理它们。

进程内不合作的初始化、Handler 或 disposer 无法被 Go 强杀。等待超时返回 blocked 错误，实例保持 QUIESCING，provider 的依赖资源不会提前释放。外部 Runtime 可用自身强制终止机制；Engine 不伪造强制停止，也不回滚任意外部 I/O。

## 事件与 middleware

事件默认在声明 Scope 所在 realm 及后代可见，不向兄弟或祖先泄漏。Sequential 按登记顺序执行并首错停止；Parallel 最多同时调度八个监听者，仍保持同一实例回调串行，等待全部后返回可归属监听者的聚合错误；First 只用明确的 Handled 标志短路，`0`、`false`、空字符串都是有效值。停用拒绝新回调并等待已经准入的回调结束。

公共类型化入口为 `contract.EventKey[T]{Name, Mode, Validate}`、`contract.Subscribe(scope, event, handler)` 和 `contract.Publish(ctx, emitter, event, input)`。事件声明显式选择 Sequential、Parallel 或 First；Subscribe 在调用监听者前严格解码数据并执行可选 Validate，Publish 在编码前执行 Validate。监听者返回的 `contract.EventResult.Handled` 与 Value 分开表达，不能根据 Value 的真假判断是否处理。Scope/SDK 使用相同窄接口，类型化 API 不改变 realm、串行准入或停用等待语义。

`contract.Chain(final, middlewares...)` 是公共 middleware 入口，按传入顺序包裹 Handler；每个 middleware 在一次调用中只能执行一次 next，第二次返回 `reentrant_call`，下游不重复执行。插件作者不需要导入 `internal/core`。

## 外部贡献与发布门控

`Hooks.BeforePublish(identity)` 在锁外准备与 activation 关联的外部记录；失败会阻止发布并进入统一清理。`OnRetire(identity)` 在锁外撤销记录，且此时 `IsActive(identity)` 必为 false。每个 activation 的两个 hook 互斥串行：退役若先发生，迟到初始化会跳过 BeforePublish；若预备已开始，撤销等待它返回，避免迟到预备记录重新出现。普通取消不被 hook 阻塞。

独立 registry 的锁无法凭两个普通回调实现跨锁原子提交。外部贡献的读取必须先取得 snapshot、释放自己的锁，再用 `Engine.IsActive(identity)` 筛选；该 active 标志就是服务与贡献共同的可见提交点。不要在外部 registry 锁内回调 Engine，避免 ABBA 锁顺序。
