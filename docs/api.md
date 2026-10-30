---
title: Go API 导航
description: 当前公开入口与职责。
---

# Go API 导航

| 包 | 主要入口 |
|---|---|
| `cordis` | `Open`、`Host.AddBuiltin`、`Install`、`Select`、`Enable`、`Call`、`Caller`、`Emit`、`Close` |
| `contract` | `Definition`、`Entry`、`Scope`、`Instance`、服务与事件类型 |
| `sdk` | `Run`、类型化 `Provide` / `Call`，以及显式流测试入口 `RunTransport` |
| `schema` | 声明解析、配置验证、服务与配置生成、编辑器 schema 导出 |
| `packagehost` | 包构建、签名、验证、安装、选择与运行记录 |
| `runtime/native` | 启动和回收标准协议原生进程 |
| `plugintest` | 真实 SDK、业务服务测试端口与可等待清理 |
| `config` | TOML、YAML、JSON 的严格配置读取 |

## Definition 与 Scope

`Definition.Children` 直接声明 builtin 可用子定义，不需要第二次全局登记。`contract.CreateChild` 为 builtin 和 native 返回子实例句柄。

`Scope` 提供 Identity、Config、Context、Defer、Provide、Call、On、Emit。`contract.Go` 登记显式并发工作；`contract.Own` 接管可关闭资源。

## 插件包

`contract.Manifest.Resources` 是额外运行普通文件的精确路径清单，省略表示空集合。`packagehost.Pack` 发布它们与声明产物，不把整个开发目录放进 ZIP；构建证据仍在私有输入快照校验。

`packagehost.Inspect(ctx, archive)` 返回 `Inspection`：内嵌 `Package` 元数据，`Files` 为验证后的完整文件清单（Path、Size、SHA256）。检查不授予信任，也不安装或执行。安装 catalog 仍只存 Package，不重复保存检查清单。

`packagehost.LoadAuthor` 的结果提供 `Inputs()`，返回已解析作者源与契约文件的相对路径副本，供开发工具明确监视这些输入。

## 事件订阅

```go
handle, err := scope.On(ctx, "changed", contract.SubscriptionOptions{
    Once: true, Prepend: true,
}, handler)
// 类型化入口：
handle, err = contract.Subscribe(ctx, scope, eventKey, options, typedHandler)
// 需要提前取消时：
err = handle.Cancel(ctx)
```

On 可在初始化及 Active 期间调用；初始化使用 Start 收到的 ctx，以保留跨进程父调用关系。初始化失败不会发布监听者。Active 登记成功代表 Host 已确认，回调可能与 On 的返回并发；不要依赖返回后的变量赋值才能运行回调。

普通监听者按登记顺序执行，prepend 插入前方，多个 prepend 中最后登记的优先。Sequential 遇错停止；Parallel 最多并发 8 个投递并等待全部结果，同实例仍串行；First 遇到显式 `Handled=true` 停止，Value 为 false、0 或 null 都不改变该规则。builtin 和 native 都返回逐监听者结果，事件范围是 Host 广播。

once 在准入时消费，回调失败也不会重试。Cancel 只撤销后续准入，已准入回调可以完成；已取消或 once 已消费后的重复 Cancel 成功。每次激活最多 `contract.MaxSubscriptions`（256）个活动订阅，取消或消费释放名额。退役自动移除订阅并等待已准入回调，通常无需额外 Defer。完整可运行示例见 `events_example_test.go`。

自定义 `contract.Session` 必须实现 `CallWithReceipt`：回调在请求进入发送顺序时报告 true，确定未发送时报告 false，恰好调用一次且先于响应等待。回调须及时返回；该接口用于取消顺序，不提供新的异步业务 API。

## Instance

实例提供 `Status`、`Wait`、`Update`、`Stop`。Update 返回 revision、Applied、Superseded；取消等待只结束等待。远端子实例的 Status 是最后收到的快照，`Refresh(ctx)` 获取当前状态。

Host 的 `Instance(id)` 和 `Instances()` 返回当前登记。持久控制、恢复和包管理的区别见[宿主接入](host-guide.md)。

`Instances()` 也包含动态子实例：`Origin` 为 `persistent`、`builtin` 或 `dynamic`，动态项的 `Owner` 是创建它的完整父激活身份。动态句柄的 Update/Stop 不写入持久 entry；父重建后，旧句柄不能操作同 ID 的新实例。清理中的子实例保留为 Quiescing，移除完成后才消失。

## 服务解析视图

`Entry.Isolate` 和 `Child.Isolate` 是完整 key 列表，例如 `[]string{"db@1"}`。指定的 key 在该登记处建立边界，由子树继承；嵌套声明覆盖该 key 的外层边界，缺 provider 不向外回退。未声明的 key 继续共享最近祖先边界或根空间。它是组合路由，不是权限沙箱。

`Host.Call` 查询根空间；`Host.Caller(entryID) (contract.Caller, error)` 捕获当前登记的解析视图，可传给生成的服务客户端。它不会冒充插件，也没有 cleanup bypass；配置更新和重新激活不改变视图归属，移除重建同 ID 后旧视图失效。边界随 entry 登记固定，改变声明须 Disable/Enable 重建。

隔离属于宿主 entry/动态 child，不写进包的 definition 或生成的服务契约。静态声明随意图持久恢复；动态声明随父 activation 重建。事件仍按 Host 广播。

## 诊断与关闭完成

`cordis.Options.Logger` 和 `packagehost.Options.Logger` 接受可选 `*slog.Logger`；nil 保持静默。handler 在状态锁外同步执行，必须及时返回，不得重入管理或关闭方法。框架记录生命周期、启动耗时、失败调用和包操作结果，不默认复制配置或调用输入输出。用户错误文本可能包含业务数据，框架不自动脱敏。

`Status.Outcome` 与 `RunRecord.Outcome` 使用 `contract.Outcome`：Reason、可选 ExitCode/Signal、Forced、StartupDuration，以及分开的 FailureCode/CleanupCode。未知业务错误码归为 PluginFailed，原错误链保留在当次返回值和日志。Status 在终态仍保留最近 activation 身份；历史记录只保存固定结构结果，不保存原始错误文本或栈。

`contract.ProcessSession.Outcome()` 在 Done 后返回最终进程事实；没有退出码不等于退出码 0。`Options.Stderr` 接收 `contract.StderrRecord`，包含 Owner、Data、Truncated。超额输出仍排空，并报告一次截断。`CapturePanic` 只把 builtin 栈输出到显式配置的 Logger，不加入 Status 或历史记录。

`Host.Done()` 在激活完全排空且 Store 关闭后结束。Close 的错误与完成分别判断：历史失败或清理失败可以与完成同时存在；等待超时则不能当作已关闭。之后可使用新的 context 再调用 Close。

## 失败重试与恢复

`Host.Retry(ctx, entryID) error` 原子检查当前静态 Failed 登记及清理完成情况，以现有配置提交新 revision，不重写持久意图。超时只取消等待；并发 Update、Stop 或替换不会被旧重试覆盖。

`Options.Recovery map[string]RecoveryPolicy` 显式选择已有持久 native entry；policy 的 MaxRetries、InitialDelay、MaxDelay 控制有限退避，Open 拷贝并校验。`Host.RecoveryStatus() []RecoveryInfo` 返回会话内的计数、状态、身份/revision 和可选 NextRetryAt，返回值不会与内部调度共享可修改数据。默认无自动恢复，详见[恢复](recovery.md)。

## 错误

使用 `errors.Is` / `errors.As` 查看 context 错误、`contract.Error` 和包提交错误。`packagehost.CommitError` 表示提交点已跨过，不应当按“未发生”重复操作。
