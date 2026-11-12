---
title: 组合与生命周期
description: 依赖激活、隔离、配置更新、父子所有权与清理顺序，以及与官方 Cordis 的能力对照。
---

# 组合与生命周期

本页写多个实例组合在一起时的规则：谁先启动、谁先清理、隔离怎么划、更新怎么收敛。词汇见[核心概念](concepts.md)，完整可运行的例子见[组合实操](composition-tutorial.md)。进程内插件与 native 实例由 Host 放在同一个内核里，下面的规则对两者一致。

## 依赖激活

实例声明 `Requires`，全部兼容的 provider 发布后执行 `Start`。等待原因可通过 `Status` 查看：缺少服务、版本不兼容、父尚未发布。同一解析空间里的重复服务返回冲突。

消费者只使用本次激活的绑定。provider 退役会撤销普通准入、通知取消并让消费者退役；C → B → A 的清理按 C、B、A 完成。清理期间仍可调用原 provider，不会把清理请求发给替换后的新实例。

## 按服务隔离

在父 entry 上设置 `Isolate: []string{"db@1"}`，这棵子树里的 provider 与消费者使用自己的 `db@1`；未列出的服务继续共享。嵌套的子实例可以再声明同一个 key，建立下一层边界。`db@1` 与 `db@2` 独立，不能只写 `db`。进程内用 `s.Isolate(key)` 得到边界内的作用域，见[进程内内核](kernel.md#更多能力)。

边界内缺 provider 时保持 Waiting，不选择边界外的同名服务。provider 的更新、停止与恢复只沿实际绑定影响消费者，清理仍访问原绑定；另一组不会因为同名服务一起重启。

应用通过 `view, err := host.Caller(entryID)` 取得某个 entry 的解析视图，用 `view.Endpoint(required)` 取得 endpoint 交给生成的客户端；`host.Call` 只查根。视图属于取得时的那次登记，entry 停用、移除或被替换后失效；它不是插件身份，没有清理特权。边界在登记时固定，`Update` 不移动它；持久 entry 的声明随 entry 保存，动态子实例的声明随父激活重建。事件仍广播到整个 Host，隔离也不限制进程权限。

## 配置更新

`Update` 在提交前校验配置，无效配置不改变运行实例，也不写持久记录。有效更新增加期望 revision，清理当前激活后再启动；并发更新收敛到最新配置，较早的等待者得到 `Superseded`（进程内为 `ErrSuperseded`）。

业务初始化失败后保持 Failed。再次提交有效配置（包括相同的配置）会重新启动；Host 的持久 entry 在完成清理后也可用 `host.Retry` 保持配置重试。持久 native 实例可显式启用[有限自动恢复](recovery.md)。所有路径都不回滚副作用，也不重放业务调用。

## 父子所有权

进程内的子实例是在父实例作用域里 `Load` 的插件；native 的子实例由 `sdk.CreateChild` 向 Host 登记同包的另一个定义，持久 entry 用 `Entry.Parent` 指向父。父重新启动后，旧的 `Scope` 不能再创建子实例，旧句柄也不控制新实例。

父的 `Start` 可以登记子实例，但不能同步等待它启动。父退役时，包括仍在 Waiting 的子实例都被移除；子失败不会自动停止父。

## 并发与取消

服务方法、事件监听、查询处理者和链中间件都可能并发调用，实现要自己保护共享状态。`Go` 登记的后台工作在实例 Active 后开始，退役时先取消，清理等待它们返回。

服务调用不做环检测：处理函数并发执行，A 调 B、B 再回调 A 只是又一次调用，不会排队死锁；跨进程的回调经 `parent` 继承原请求的取消与期限。生命周期等待的重入由内核检测：在回调里等待调用链上的实例、它的祖先或它依赖的 provider 停止，得到 `ErrReentrant`，见[进程内内核](kernel.md#状态与失败)。

## 与官方 Cordis 的对应

对照使用只读参考提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88`，对齐的是能力和语义，不是 API 的形状与名称，也不能加载官方的 TypeScript 插件。“进程内”指 `cordis` 包的键；“native”指跨进程的 `contract` 键，传递按契约校验的 JSON，在 Host 的内核里与进程内插件互通。

| 官方能力 | 进程内 | native |
|---|---|---|
| `ctx.plugin`、fiber 树 | `Plugin[C].Load(scope, config)`，实例树任意深度 | 持久 entry 的父子关系，`sdk.CreateChild` 的子实例 |
| 活对象服务 `ctx.provide` / `inject` | `Service[T]` 的 `Provide`、`Get`，`Requires` 声明 | `contract.EndpointKey` 与 `Require`，生成的类型化服务，按名称与 major 解析、minor 区间校验 |
| `ctx.inject(deps, callback)` | `Scope.When` | 插件进程内可用 `Scope.When` |
| `Service.check` | `Service[T].Where` 与 `Changed` | minor 区间 |
| `ctx.isolate` | `Scope.Isolate`、`IsolateIn` | entry 与 `Child` 的 `Isolate`，登记时固定 |
| `ctx.intercept` | `Setting[O]` | 无；设置只在插件进程内可见 |
| `on`、`once`、prepend | `Event[T].On` 与 `Once`、`Prepend` | `EventKey[T].On`，同样的选项，返回可撤销的 `cancel` |
| `emit`、`parallel` | `Emit`、`EmitParallel` | `EventKey` 的 `Emit`、`EmitParallel` |
| `serial`、`bail` | `Query[In, Out]` 的 `Ask`，显式 ok | `QueryKey` 的 `Ask`、`Handle`，显式 handled |
| `waterfall` | `Chain[In, Out]` 的 `Use`、`Do` | `ChainKey` 的 `Use`、`Do`，每个跨进程中间件两次往返 |
| 流式结果（官方没有） | 服务方法返回 `iter.Seq2[T, error]` | 同样的方法签名，有窗口与额度 |
| 事件过滤、global | `Scope.Filter`、`Global` | 无，事件在 Host 内广播 |
| `ctx.effect` 与撤销函数 | `Scope.Defer`、`Scope.Sub` | `Scope.Defer` |
| `fiber.update`、`internal/update` | `Instance.Update`、`OnUpdate` | `Instance.Update`，总是重建激活 |
| `internal/status`、`internal/service` | `InstanceChanged`、`ServiceChanged` | 状态查询与运行记录 |
| `ctx.logger`、exporter | `logging` 包，基于 `log/slog`，见[日志服务](logging.md) | 插件进程内可加载 `logging`；Host 有可选 slog 诊断日志与 stderr 转发 |
| `ctx.timeout`、`interval`、`throttle`、`debounce` | `timer` 包的函数，见[定时辅助](timers.md) | 插件进程内可用 |
| HMR | 无 | `cordis dev` 构建、预检后重启，不保留进程内存 |
| loader、include、group、配置写回 | `loader` 包：配置文件到实例树、运行期编辑、保留注释的 YAML 写回、嵌套文件与补丁，见[配置文件加载器](loader.md) | 持久 entry 保存配置及父子关系，不写回源配置；与加载器文件是两份独立的意图 |

内核与官方的有意差异：

| 官方行为 | 内核 |
|---|---|
| fiber 顶层的清理并发执行 | 按登记逆序顺序执行 |
| provider 变化时不取消消费者进行中的加载 | 取消它的 `ctx` |
| 只有 `update` 能恢复失败的 fiber | `Update` 和 `Restart` 都能 |
| 根 dispose 等于重启 | `App.Close` 是终态 |
| 被取代的更新等待者得到最新结果 | 得到 `ErrSuperseded` |
| 加载中收到更新时调用监听而不重启 | 取消并以新配置重新启动 |
| 服务方法经 Proxy 得到调用方上下文 | 服务方法显式接收调用方的 `*Scope` |

Go 中不提供的官方能力：Proxy 式 `ctx.foo` 属性访问、mixin、accessor、`ctx.set`、非根作用域上不带声明的宽松查询、可调用服务、按插件索引的 registry、插件从内部停止或更新自己、`internal/dispatch` 钩子、effect 的自省、保留内存状态的 HMR、加载 TypeScript 插件。

官方参考实现可定位到 [context](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/context.ts)、[events](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/events.ts)、[HMR](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/hmr) 和 [include](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/include)。Host 的 `Replace` 会清理并重建激活，旧句柄不控制新实例；离线 CLI 不控制正在运行的 Host，[本机在线控制](local-control.md)须显式开启，[有限恢复](recovery.md)须按实例配置。

[完整组合程序](composition-tutorial.md) · [资源释放](resources.md)
