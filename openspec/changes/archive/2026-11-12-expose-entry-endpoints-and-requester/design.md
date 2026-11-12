## Context

源码事实（`7cb8d3e`）：

- `hosting.Caller` 只保存 entry 的作用域（`hostInstance.scope`，根激活的作用域加上 entry 及其上层的隔离空间）。`Caller.Call` 每次用 `contract.EndpointKey(...).Get` 解析并校验版本，再调用 `Call`；没有流，也拿不到 endpoint。生成的 `GetX(s)` 只接受 `*cordis.Scope`，`NewXClient(endpoint)` 只接受 `contract.Endpoint`。
- `Host.Caller` 从 `h.entries` 取登记。停用、移除与替换经 `retireOne`/`forget` 删掉登记，再启用得到新的 `hostInstance` 和新的隔离空间；恢复重启沿用原登记。旧 `Caller` 的作用域仍属于根激活：隔离的服务落在已经清空的旧空间里失败，未隔离的服务照常解析到根空间。
- native 激活发来的请求由 `run.handle`（调用、事件、订阅、链、子实例、`failed`）与 `run.stream`（流式调用）处理。`run` 持有激活身份 `contract.Identity{Package, Entry, Activation, Digest}`，但处理者拿不到。`runtime/native` 与 `runtime/memory` 都把这两个函数交给 `wire.New`，所以在这里加的信息与运行时无关。
- `wire` 收到带 `parent` 的请求时，从父请求的 ctx 派生处理 ctx：取消、期限和 ctx 上的值都随之继承。`parent` 还决定初始化与关闭期间的准入。没有调用环检测；处理函数返回的非编码错误一律以 `plugin_failed "handler failed"` 发出，内核的 `ErrReentrant` 不会成为 `reentrant_call`。

## Goals / Non-Goals

**Goals:**

- 应用能在某个 entry 的视图里取得经过版本校验的 endpoint，交给生成的客户端，一元和流式方法都能用。
- 旧视图在 entry 的登记消失或被替换后明确失败。
- 宿主侧处理者能知道请求来自哪个 native 激活，两种运行时一致。
- 协议文档如实描述 `parent`。

**Non-Goals:**

- 授权、身份校验或按插件限流。
- 把请求方身份带过线协议，让插件 B 知道 A 调用了宿主。
- 调用环检测。
- 让 `host.Caller` 覆盖动态子实例。

## Decisions

### 1. `Caller.Endpoint` 返回当时的 provider

```go
func (c Caller) Endpoint(required contract.Requirement) (contract.Endpoint, error)
```

在视图里解析 `required.Name@required.Major`，版本不在范围内时返回 `*contract.VersionError`，没有 provider 时返回 `not_ready`（与 `Host.Call` 现有的错误相同），否则返回 provider 本身。语义与根作用域上的 `Service.Get` 一致：endpoint 指向调用这一刻的 provider，不随 provider 重启切换；native provider 停止后在它上面的调用以 `not_ready` 失败（会话已关闭或在调用中结束），应用重新取。`Caller.Call` 改为 `Endpoint` 加 `Call`，行为不变。

拒绝的备选：

- 返回每次调用都重新解析的"活" endpoint：生成的客户端在构造时检查 `Service()`，活 endpoint 的版本会随 provider 变，两次调用可能落到不同版本；根作用域也没有这种 endpoint，两套语义只会让应用困惑。
- 让 `Caller` 暴露 `*cordis.Scope`，复用生成的 `GetX`：作用域可以 `Load` 插件、`Provide` 服务、`Defer` 清理，交出去等于把根激活的生命周期权限交给持有视图的代码，而视图只应用于解析。
- 新增 `Caller.Stream`：只补了流，生成的客户端仍然用不上，`Endpoint` 一次覆盖两者。

### 2. `Caller` 绑定那次登记

`Caller` 改为保存 Host、entry 标识与取得时的 `*hostInstance`。`Endpoint` 与 `Call` 先在 Host 锁内确认 `h.entries[id]` 仍是这个实例，否则返回 `not_ready`，错误文本带 entry 标识。恢复重启不替换实例，视图继续有效；停用、移除和 `Replace` 都会替换或删除登记，旧视图随之失效，与 `host.Instance(id)` 的旧句柄规则一致。

失败场景是具体的：应用缓存了视图，entry 被移除后又以同一 ID 启用，旧视图对未隔离的服务继续成功，对隔离的服务以含糊的"服务不可用"失败，应用无从知道自己拿着过期的视图。

已经交出的 endpoint 不受这项检查约束，它跟随自己的 provider：native provider 随 entry 停止时，调用以 `not_ready` 失败。

### 3. `Requester(ctx)` 由 bridge 在请求入口标出

```go
func Requester(ctx context.Context) (contract.Identity, bool)
```

`run.handle` 与 `run.stream` 在处理任何请求之前用 `context.WithValue` 把 `r.identity` 放进 ctx。键是 `hosting` 的私有类型，只有 `Requester` 能读。于是：

- native 激活调用的进程内服务、流式方法，触发的事件监听者与查询处理者，发起的链，都在 ctx 上看到这个激活。
- 处理者把 ctx 继续传给其他进程内服务时，请求方跟着传下去：请求仍然来自那个激活。
- 处理者调用另一个 native 插件 B，B 再回调宿主时，`run.handle` 用 B 的身份覆盖：最近的请求方总是直接发来请求的那个激活。
- 应用自己的调用、进程内插件的调用，ctx 上没有这个值，报告 `false`。进程内插件没有包、entry 与摘要，不为它伪造身份；进程内需要调用方时，按 `docs/kernel.md` 的建议显式传作用域。

不改线协议：身份由 Host 在启动激活时分配，`run` 已经持有，不需要插件在帧里声明，插件也无法冒充别的激活。

拒绝的备选：

- 只在 `call` 上标记：事件与链同样由插件发起，宿主处理者面对的是同一个问题，分开处理只多出条件。
- 放进 `contract` 包，作为跨包的 ctx 约定：只有 Host 会写入它，读它的是宿主应用，放在 `hosting` 让依赖方向保持"应用 → hosting → contract"。
- 在线协议的请求里带调用方身份：插件可以填任何值，宿主还得校验，而 Host 本来就知道是哪条会话。

### 4. `parent` 不做环检测，更正文档

初版设计用每实例串行回调，环会死锁，所以需要检测并以 `reentrant_call` 拒绝。`rebase-native-on-kernel` 改为处理函数并发执行、去掉串行槽，A 调 B、B 再回调 A 只是又一个并发请求，不会阻塞；那次 change 明确写了"没有调用环检测"，文档没有跟着改。现在再加检测，会拒绝合法且已在下游使用的回调（插件处理调用时回调宿主服务，宿主服务再调用这个插件）。

所以只更正文档：`parent` 把请求关联到正在处理的请求，被关联的请求继承它的取消与期限，并参与初始化与关闭期间的准入；回调照常处理。`parent` 指向已回答的请求时以 `canceled` 结束（现有行为）。不带 `parent`（ctx 不来自正在处理的请求）的请求作为独立请求处理。`docs/composition.md` 中"普通调用与生命周期等待都参与环检测……跨进程 `reentrant_call`"同样更正：生命周期等待的重入检测由进程内内核提供，普通调用不做环检测。

## 模块归属与依赖方向

改动只在 `hosting`：`host.go`（`Caller`）与 `bridge.go`（标记请求方、`Requester`）。`contract`、`wire`、`sdk`、`runtime/*` 不变。依赖方向不变：应用 → `hosting` → `contract`/`cordis`。

## Risks / Trade-offs

- [旧 `Caller` 对未隔离服务由成功改为 `not_ready`] → 这是对规格"旧视图明确失败"的更正；cmd/cordis 中跨恢复重启复用视图的用法不受影响，因为恢复重启不替换登记。
- [请求方随 ctx 传播，处理者用这个 ctx 做的所有进程内调用都报告同一个请求方] → 这符合"请求来自谁"的含义；文档写明它只是信息。需要区分"直接调用方"的应用应在自己的边界处读取。
- [处理者为后台工作保留请求 ctx 的值（例如 `context.WithoutCancel(ctx)`）时，后台工作仍带着请求方] → 这是 Go ctx 的通常语义，文档不另作承诺。
- [`Requester` 对进程内插件报告 `false`，应用可能误以为"没有请求方"就是宿主自己] → 文档写明：进程内插件与应用的调用都报告 `false`。

## Open Questions

（无）
