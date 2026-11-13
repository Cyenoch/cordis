---
title: Go API 导航
description: 按包列出公开入口，并指向各自的说明页。
---

# Go API 导航

按“我要做什么”找到对应的包与入口。签名以 `go doc` 为准，行为说明在右列链接的页面里。

| 包 | 主要入口 | 说明 |
|---|---|---|
| `cordis` | `New`、`App`、`Plugin[C]`、`Instance[C]`、`Scope`、`Service[T]`、`Event[T]`、`Query[In,Out]`、`Chain[In,Out]`、`Setting[O]`、`OnUpdate`、`InstanceChanged`、`ServiceChanged` | [进程内内核](kernel.md) |
| `loader` | `Registry`、`Register`、`Default`、`RegisterSetting`、`Plugin`、`Service`、`Ready`、`Loader` 的 `Create`/`Update`/`Remove`/`Move`/`Reload`/`Wait`/`Entries`、`EntryOf`、`Changed` | [配置文件加载器](loader.md) |
| `logging` | `Plugin`、`Service`、`Get`、`Name`、`Level`、`Provider.AddHandler`、`Recent` | [日志服务](logging.md) |
| `timer` | `AfterFunc`、`Every`、`Sleep`、`Ticks`、`Throttle`、`Debounce` | [定时辅助](timers.md) |
| `sdk` | `Run`、`Define`、`CreateChild`、`Identity`、`RunTransport` | [编写插件](plugin-author.md) |
| `contract` | `EndpointKey`、`Require`、`EventKey`、`QueryKey`、`ChainKey`、`Endpoint`、`Entry`、`Child`、`Manifest`、`Status`、`Instance` | [数据契约](contracts.md) |
| `schema/gocontract` | `Generate`、`Load`：从 Go 包推导契约，生成绑定 | [数据契约](contracts.md#用-go-类型写契约) |
| `plugintest` | `Start`、`Options`、`Harness` | [编写插件](plugin-author.md#测试) |
| `hosting` | `Open`、`Host`、`Options` | [接入宿主](host-guide.md) |
| `schema` | `Codec`、`Parse`、`Generate`、`GenerateMany`、`Compile`、`Validate`、`Check`、`Export`、`Definition` | [数据契约](contracts.md) |
| `config` | `Load`、`Parse`、`Document` | [数据契约](contracts.md#配置文件解析) |
| `packagehost` | `Pack`、`Sign`、`Inspect`、`LoadAuthor`、`Build`、`Store`、`TrustPolicy` | [包格式](packages.md) |
| `runtime/native` | `New`：启动并回收标准协议的 native 进程 | [native 运行时](runtimes.md) |
| `wire` | 会话端点与帧；插件作者和应用通常不直接使用 | [会话协议](protocol.md) |

## 作用域与实例

插件只有一种形状：`cordis.Plugin[C]`，`Start` 收到 `*cordis.Scope`。`Scope` 提供 `Defer`（清理）、`Go`（随激活的后台工作）、`Sub`、`When`、`Isolate`、`IsolateIn`、`Filter`、`Info` 等；`Plugin.Load(scope, config)` 返回 `Instance[C]`，提供 `Status`、`Wait`、`Update`、`Restart`、`Stop`、`Done`。native 入口是 `sdk.Run(ctx, sdk.Define(<P>Plugin))`，`<P>Plugin` 是生成的 `cordis.Plugin[Config]` 值，Host 应用用 `Load` 把同样的插件加载到 `host.Scope()`。

Host 管理的实例用 `contract.Instance`：`Status`、`Wait`、`Update`（返回 `UpdateResult`，含 revision、Applied、Superseded）、`Stop`。取消等待只结束等待，不撤销已提交的操作。远端子实例（`sdk.CreateChild` 返回的 `contract.ChildInstance`）的 `Status` 是最后收到的快照，`Refresh(ctx)` 获取当前状态。

## 跨进程能力

由键的类型决定一个能力能否跨进程：

| 能力 | 进程内 | 跨进程 |
|---|---|---|
| 服务 | `cordis.Service[T]` | `contract.EndpointKey(name, major)` + `contract.Require(key, min, max)`；生成的 `Provide<Svc>`、`Get<Svc>`、`<Svc>Endpoint`、`New<Svc>Endpoint`、`New<Svc>Client` |
| 事件 | `cordis.Event[T]` | `contract.EventKey[T]` |
| 查询 | `cordis.Query[In,Out]` | `contract.QueryKey[In,Out]` |
| 中间件链 | `cordis.Chain[In,Out]` | `contract.ChainKey[In,Out]` |

跨进程键的 `On`/`Handle`/`Use` 返回 `cancel func(context.Context) error` 和 error，因为在 native 进程里登记是一次往返；`cancel` 只撤销后续准入，已准入的回调可以完成，重复调用成功。登记选项 `cordis.Once`、`cordis.Prepend` 与进程内相同；每次激活最多 `contract.MaxSubscriptions`（256）个活动登记，退役时自动移除。Host 应用在 `host.Scope()` 上直接使用这些键。

## Host

`hosting.Host`：

- 作用域与调用：`Scope`、`ProvideParent`、`Call(ctx, requirement, method, input)`、`Caller(id)` 与 `Caller.Endpoint(required)`、`Emit(ctx, contract.Event)`、`Resolver()`；包级 `Requester(ctx)` 报告 native 请求方，见[接入宿主](host-guide.md#调用与事件)。
- 包：`Install`、`Select`、`Replace`、`Uninstall`、`Store`。
- 实例：`Enable`、`Disable`、`Remove`、`Resume`、`Retry`、`Instance`、`Instances`、`Status`、`Restore`、`Close`、`Done`。
- 恢复：`RecoveryStatus`、`SetRecovery`、`ClearRecovery`，启动时的策略由 `Options.Recovery` 给出。

各操作的语义见[接入宿主](host-guide.md)、[恢复](recovery.md)；组合规则（隔离、清理顺序）见[组合](composition.md)。`Options.Logger` 与 `packagehost.Options.Logger` 接受可选的 `*slog.Logger`，nil 保持静默；handler 在状态锁外同步执行，须及时返回，不得重入管理或关闭方法。

## 状态与结果

`contract.Status` 含身份、`State`、`Enabled`、`TemporaryStopped`、`Desired`/`Applied` revision、`Waiting` 原因和 `Outcome`。`contract.Outcome` 记录 `Reason`、可选的 `ExitCode`/`Signal`、`Forced`、`StartupDuration`，以及分开的 `FailureCode` 与 `CleanupCode`；未知的业务错误码归为 `plugin_failed`，原始错误链只保留在当次返回值和日志。终态仍保留最近一次 activation 的身份。`packagehost.RunRecord` 只存固定结构的结果，不存原始错误文本或栈。

`contract.ProcessSession.Outcome()` 在会话结束后返回进程的最终事实；没有退出码不等于退出码 0。`Options.Stderr` 收到 `contract.StderrRecord`（`Owner`、`Data`、`Truncated`），超额输出仍被排空，并只报告一次截断。

## 错误

用 `errors.Is` / `errors.As` 判断 context 错误、`*contract.Error`（`Code` 与 `Message`）、`*contract.VersionError`，以及包提交错误。`packagehost.CommitError` 表示提交点已经跨过，不应当按“未发生”重复操作。
