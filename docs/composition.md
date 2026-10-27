---
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - host.go
  - children.go
  - instance.go
  - service.go
  - cascade.go
  - ownership.go
  - contract
  - internal/core
  - sdk/client.go
  - sdk/child.go
---

# 组合、实例与清理

插件通过 `contract.Scope` 发布服务和登记资源，宿主使用公开 `cordis.Host` 管理实例。`internal/core` 是 Host 内部的生命周期引擎，外部项目不能导入。本地类型化服务由根包包装成 JSON Handler。完整程序见[组合实操](composition-tutorial.md)。

## 宿主入口

常用操作如下：

- `cordis.Open(Options)` 打开独占包存储；`AddBuiltin(ctx, entry, definition)` 注册进程内定义，`Enable(ctx, entry)` 启用已选择的外部包。
- `ConfigureRealm(name, parent, isolatedKeys)` 配置作用域。局部 provider 优先；隔离 key 不继承父服务。key 为 `Service.Key()`，同 realm 同 key 不允许重复发布。
- `Call(ctx, realm, requirement, input)` 与 `Emit(ctx, realm, name, input, mode)` 仅使用已发布服务和订阅。
- `Instance.Stop` 停止但保留实例；`Host.Resume(ctx,id)` 重新启用指定的已完全停止实例，静态子实例仍分别 Resume，作者动态子实例则随 Start 重建；失败状态用 `Instance.Retry`。`Host.Disable(ctx,id)` 清理并移除整棵静态所有权子树，释放 ID，之后用 AddBuiltin/Enable 重建。
- `Replace(ctx, ref, options)` 先预检，再退役并重建目标包及其静态子树。反向依赖不被隐式关闭；需要时使用显式级联 API。
- `Close(ctx)` 排空后释放包目录。等待取消不等于资源已经释放；管理队列上的调用也遵守各自 context。

Entry ID 在同 Host 内唯一。相同 Definition 可用不同 Entry/Config 多开。每次真实启动生成新的 `Identity.Activation`，不复用旧会话权限。

### 作者创建可选子实例

Start 内调用公开 `contract.CreateChild(ctx, scope, contract.Child{ID:"optional", Definition:"feature", Config: config})`，SDK 与 builtin 使用同一 API。外部子定义必须来自父 activation 的同一包版本；builtin 先由宿主 `RegisterDefinition(ctx, packageID, definition)` 注册。每个 activation 最多拥有 256 个直接子实例。

CreateChild 只登记，不等待启动。父实例发布后子实例才可启动，子功能缺依赖或失败不会阻止父实例 Active。初始化中 Wait 自己的子树会立即返回 `reentrant_call`。初始化返回后可用 `ChildInstance.Wait/Update/Retry/Stop` 管理；`Refresh(ctx)` 读取最新状态，跨进程 `Status()` 为上次确认的快照。

作者子实例 ID 为 `parentEntry.localID`，绑定当前 activation，不持久保存为独立启用 entry。父退役会停止并移除子树，重启父时由新的 Start 重新创建。旧 Scope/远程 handle 不能操作新 activation 的子实例。独立持久子实例由宿主设置 `Entry.Parent` 后 Enable/AddBuiltin；builtin 父锚点持久保存，但应用必须在 Restore 前重建 builtin 回调和配置。

## 配置与依赖快照

Definition 的 `Requires` 是当前 activation 的必需服务列表，`Provides` 是初始化必须完成的精确声明。所有注册在 Start 中暂存；Start 成功、声明齐全、无重复 provider 且 revision 仍为最新时才整体发布服务和订阅。消费者初始化前取得固定 binding snapshot，不会在调用中悄悄切换 provider。provider 退役后消费者退回等待，新的 provider 发布后生成新的消费者 activation。

`Instance.Status` 分别报告 Desired、Applied、WaitingFor 和最后错误。`Update` 先校验再增加 Desired，取消其等待不撤销有效更新。更新会停止旧 activation，最终只应用最新版本；被覆盖的等待者收到 `UpdateResult.Superseded`。旧初始化即使忽略取消并迟到完成也不能发布。失败实例只有有效 Update 或显式 Retry 才恢复。确定的服务激活依赖环被诊断为 `reentrant_call`。

Facade 的 `Status.Diagnostics` 保留包预检中可选依赖缺失/不兼容的诊断，不将其混成启动失败；必需服务未满足仍由 WaitingFor 表达。配置提交和 Stop/Remove/Close 的生命周期准入有序协调：停用之后拒绝的更新不能迟到写入 catalog，也不能复活已关闭实例。真实外部链与对应并发回归见[生命周期验收](lifecycle-acceptance.md)和[回归](regressions.md)。

## 所有权与停止顺序

一个 Scope 对应一个 activation，拥有服务、订阅、固定 binding 和逆序串行资源组。`Defer` 的 context 不受普通工作取消影响，初始化失败也会执行已登记的清理。builtin Scope 和显式 `contract.Group` 会聚合清理函数的返回错误与 panic，并继续执行其余清理。外部 SDK 的直接 `scope.Defer` 不捕获 panic；需要这个保证时，把清理登记到 Group，再将 Group 的关闭操作交给 Scope。

退役先在协调锁内撤销普通准入并取消完整传递依赖闭包的普通工作；之后锁外等待消费者和子实例清理，最后释放 provider 自身资源。消费者 disposer 只能用它已经拥有的 Scope binding 调用退役中的 provider，不可通过 Engine.Call 查找退役服务或临时获取新 binding。此清理资格会沿已有绑定的同步调用链传递。没有跨实例全局 LIFO；服务依赖与父子所有权优先于登记时间。

Facade 跟踪 external 与 builtin 的同一父子所有权树。`Host.Disable(parent)` 先移除全部子实例，再移除父实例；builtin 不写入包 catalog。替换外部父包会保存并重建其 builtin 子定义，使用新的 activation。只有外部 entry 的期望配置持久化；应用重开时仍须显式注册自己的 builtin。

业务 Handler、事件回调和初始化默认占用本 activation 的串行执行槽；控制、取消、协调不占业务槽。等待图同时覆盖同步调用链及分别占用不同实例的独立根调用：形成环的新增等待边立即返回 `reentrant_call`。等待结束或取消后删除边，不自动重入。用户自行创建的裸 goroutine 不在 Engine 可等待业务集合中，作者必须以 Defer 管理它们。

进程内不合作的初始化、Handler 或 disposer 无法被 Go 强杀。等待超时返回 blocked 错误，实例保持 QUIESCING，provider 的依赖资源不会提前释放。外部 Runtime 可用自身强制终止机制；Engine 不伪造强制停止，也不回滚任意外部 I/O。

## 事件与 middleware

订阅接收同 realm 及其后代发出的事件：从子 realm 发射会通知子 realm 与祖先的监听者，从根发射不会通知子 realm，也不会横向通知兄弟 realm。Sequential 按登记顺序执行并首错停止；Parallel 最多同时调度八个监听者，仍保持同一实例回调串行，等待全部后返回可归属监听者的聚合错误；First 只用明确的 Handled 标志短路，`0`、`false`、空字符串都是有效值。停用拒绝新回调并等待已经准入的回调结束。

公共类型化入口为 `contract.EventKey[T]{Name, Mode, Validate}`、`contract.Subscribe(scope, event, handler)` 和 `contract.Publish(ctx, emitter, event, input)`。事件声明显式选择 Sequential、Parallel 或 First；Subscribe 在调用监听者前严格解码数据并执行可选 Validate，Publish 在编码前执行 Validate。监听者返回的 `contract.EventResult.Handled` 与 Value 分开表达，不能根据 Value 的真假判断是否处理。Scope/SDK 使用相同窄接口，类型化 API 不改变 realm、串行准入或停用等待语义。

`contract.Chain(final, middlewares...)` 是公共 middleware 入口，按传入顺序包裹 Handler；每个 middleware 在一次调用中只能执行一次 next，第二次返回 `reentrant_call`，下游不重复执行。插件作者不需要导入 `internal/core`。

## 外部贡献与发布门控

普通宿主通过 `Host.Contributions()` 注册 validator，Host 已经连接下面的内部发布机制，无需导入 Hooks 或 Engine。此节解释实现边界；公开用法见[访问实操](access-tutorial.md)。

`Hooks.BeforePublish(identity)` 在锁外准备与 activation 关联的外部记录；失败会阻止发布并进入统一清理。`OnRetire(identity)` 在锁外撤销记录，且此时 `IsActive(identity)` 必为 false。每个 activation 的两个 hook 互斥串行：退役若先发生，迟到初始化会跳过 BeforePublish；若预备已开始，撤销等待它返回，避免迟到预备记录重新出现。普通取消不被 hook 阻塞。

独立 registry 的锁无法凭两个普通回调实现跨锁原子提交。外部贡献的读取必须先取得 snapshot、释放自己的锁，再用 `Engine.IsActive(identity)` 筛选；该 active 标志就是服务与贡献共同的可见提交点。不要在外部 registry 锁内回调 Engine，避免 ABBA 锁顺序。
