---
title: 组合与生命周期
description: 依赖激活、配置更新、父子所有权与清理顺序。
---

# 组合与生命周期

## 依赖激活

实例声明 `Requires`，全部兼容 provider 发布后执行 Start。等待原因可通过 Status 查看，包括缺少服务、版本不兼容和父尚未发布。同一解析空间中的重复 service key 明确返回冲突。

消费者只使用本次激活的绑定。provider 退役会撤销普通准入、通知取消并使消费者退役；C → B → A 的清理按 C、B、A 完成。清理期间仍可调用原 provider；不会把清理请求发送给替换后的新实例。

## 按服务隔离

在父 entry 上设置 `Isolate: []string{"db@1"}`，这棵子树中的 provider 与消费者便使用自己的 db@1；未列出的服务继续共享。嵌套子实例可单独声明同一 key，再建立一层边界。db@1 和 db@2 独立，不能只写 `db`。

缺少本组 provider 时保持 Waiting，不选择组外的同名服务。provider 更新、停止与恢复只沿实际绑定影响消费者，清理仍访问旧绑定；另一组不因同名服务而被一起重启。

宿主通过 `view, err := host.Caller(entryID)` 获取该登记的视图，再将 view 交给生成的客户端；默认 `host.Call` 只查询根。视图不是插件身份，不能获得清理特权。边界在登记时固定，Update 不移动边界；静态声明持久保存，动态声明随父激活重建。事件依然广播到整个 Host。

## 配置更新

Update 在提交前校验配置。无效配置不改变运行实例和持久记录。有效更新增加期望 revision，清理旧 activation，再尝试启动；并发更新收敛到最新配置，较旧等待者返回 Superseded。

业务初始化失败默认保持 Failed。再次提交有效配置，包括相同配置，会显式重新启动；静态 Failed 实例完成清理后也可用 `Host.Retry` 保持配置重试。持久 native 实例可显式启用[有限自动恢复](recovery.md)，所有路径都不回滚副作用或重放业务调用。

## 父子所有权

builtin 父定义通过 `Children` 携带可选定义；native 子定义来自父 activation 对应的同包内容。CreateChild 绑定父 activation，父重新启动后旧 Scope 不能创建新子实例。

父 Start 可以登记子实例，但不能同步等待子启动。父退役时，包括仍在 Waiting 的子实例也会移除。子失败不自动停止父。

## 并发与取消

服务和事件回调按实例串行；显式 `contract.Go` 工作是并发例外。后台工作在最终发布确认后启动，退役时先取消，资源清理等待其退出。

普通调用与生命周期等待都参与环检测。A 调 B、B 再等 A，或父 Start 等子发布，会返回可诊断错误。全局管理准入只覆盖短提交，不等待插件初始化和清理。

## 与官方 Cordis 的对应

以下对照使用只读参考提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88`，不是对官方最新版本的兼容承诺。对齐的是能力和语义，不是 API 的形状与名称，也不能加载官方的 TypeScript 插件。

仓库现在有两层，能力不同：

- **进程内内核**：根包 `cordis`，见[进程内内核](kernel.md)。
- **Host/native**：`hosting` 包和 native 插件进程，本页前面各节描述的就是这一层。它目前还没有建立在内核上，两层的插件不在同一棵实例树里。

| 官方能力 | 进程内内核 | Host/native |
|---|---|---|
| `ctx.plugin`、fiber 树 | `Plugin[C].Load(scope, config)`，实例树任意深度 | 持久 entry 的父子关系，同包的动态子实例 |
| 活对象服务 `ctx.provide` / `inject` | `Service[T]` 的 `Provide`、`Get`，`Requires` 声明 | 只有 JSON 进出的服务处理函数 |
| `ctx.inject(deps, callback)` | `Scope.When` | 无 |
| `Service.check` | `Service[T].Where` 与 `Changed` | 版本区间 |
| `ctx.isolate` | `Scope.Isolate`、`IsolateIn` | entry 的 `Isolate` 声明，登记时固定 |
| `ctx.intercept` | `Setting[O]` | 无 |
| `on`、`once`、prepend | `Event[T].On` 与登记选项 | `On` / typed `Subscribe`，返回 Cancel 句柄 |
| `emit`、`parallel` | `Emit`、`EmitParallel` | Sequential、Parallel |
| `serial`、`bail` | `Query[In, Out]` 的 `Ask`，显式 ok | First，显式 Handled |
| `waterfall` | `Chain[In, Out]` 的 `Use`、`Do` | 无；本地 `contract.Chain` 只包装一个业务 handler |
| 事件过滤、global | `Scope.Filter`、`Global` | 无，Host 内广播 |
| `ctx.effect` 与撤销函数 | `Scope.Defer`、`Scope.Sub` | `Scope.Defer` |
| `fiber.update`、`internal/update` | `Instance.Update`、`OnUpdate` | `Instance.Update`，总是重建 activation |
| `internal/status`、`internal/service` | `InstanceChanged`、`ServiceChanged` | 状态查询与运行记录 |
| `ctx.logger`、exporter | `logging` 包，基于 `log/slog`，见[日志服务](logging.md) | 可选 slog 诊断日志与 stderr 转发 |
| `ctx.timeout`、`interval`、`throttle`、`debounce` | `timer` 包的函数，见[定时辅助](timers.md) | 无 |
| HMR | 无 | `cordis dev` 构建、预检后重启，不保留进程内存 |
| loader、include、group、配置写回 | `loader` 包：配置文件到实例树、运行期编辑、保留注释的 YAML 写回、嵌套文件与补丁，见[配置文件加载器](loader.md)。改变设置、隔离或所属分组会重新加载条目；没有表达式求值 | 静态 entry 保存配置及父子关系，不写回源配置；与加载器文件是两份独立的意图 |

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

跨进程的 waterfall 与流、native 插件的 intercept、远程管理、HMR 与配置回写都还没有实现。

官方参考实现可定位到 [context](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/context.ts)、[events](https://github.com/cordiverse/cordis/blob/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/core/src/events.ts)、[HMR](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/hmr) 和 [include](https://github.com/cordiverse/cordis/tree/35fdc969e4b6c48c48ed44717c7a018324e4dd88/packages/include)。Host 的 Replace 会清理并重建 activation，旧句柄不控制新实例；离线 CLI 不控制正在运行的 Host，[本机在线控制](local-control.md) 须显式开启，[有限恢复](recovery.md) 须按实例配置。服务隔离也不限制进程权限。

[完整组合程序](composition-tutorial.md) · [资源释放](resources.md)
