---
title: 接入宿主
description: 在 Go 应用里装载 native 包与进程内插件，调用服务、管理实例并关闭。
---

# 接入宿主

Host 把进程内插件、native 包和本地包目录放进同一个内核：应用用它安装、启用、调用和关闭插件。包目录由当前 Host 独占，直到 Close 完成。

## 最小宿主

```go
host, err := hosting.Open(hosting.Options{
    Directory: storeDirectory,
    Trust: packagehost.TrustPolicy{
        Keys: map[string]packagehost.TrustedKey{
            "author-key": {PublicKey: publicKey, Publisher: "example.author"},
        },
        Publishers: map[string][]string{"example.author": {"example.counter"}},
    },
})
if err != nil { return err }
pkg, err := host.Install(ctx, archivePath)
if err != nil { return err }
if err := host.Select(ctx, pkg.Ref); err != nil { return err }
instance, err := host.Enable(ctx, contract.Entry{
    ID: "counter", Package: pkg.Ref.ID,
    Config: json.RawMessage(`{"initial":10}`),
})
if err != nil { return err }
if err := instance.Wait(ctx); err != nil { return err }
```

完整程序见 `examples/basic-host/main.go`。`Options` 的其余字段：`Runtime`（默认 native）、`FrameBytes`（默认 1 MiB）、`Inflight`（每个会话同时承载的请求数，流也占一个，16 到 1024，默认 64，其中 8 个留给会话自身；超出的调用方等待名额）、`OnCall`（[调用观测](operations.md#调用观测)）、`Grace`（插件应答取消、关闭和退出的宽限，默认 1 秒；超过即视为卡住并结束进程，负载很重的机器应调大）、`Stderr`（原始 stderr）、`Logger` 与 `CapturePanic`（[诊断](operations.md#诊断)）、`Recovery`（[有限恢复](recovery.md)）。

## 进程内插件

`host.Scope()` 是 Host 内核的根作用域。应用的插件用 `Load` 加载到它上面，与 native 实例共享同一个服务空间：两边都能依赖对方发布的端点。

```go
provider := cordis.Plugin[struct{}]{Name: "store", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
    return ProvideStore(s, store)
}}.Load(host.Scope(), struct{}{})
```

native entry 依赖 `example.store` 时，进程内 provider 一发布它就启动；provider 停止时，依赖它的 native 实例先清理再等待。反过来，进程内插件用 `GetCounter(host.Scope())` 取得 native 提供的服务的客户端。这些插件不写入包目录，随 Host 关闭而停止。`ProvideStore`、`GetCounter` 由声明了对应服务接口的包经 `cordis generate` 生成。

`host.ProvideParent(s, id)` 让进程内插件成为持久 entry 的父：`Entry.Parent` 指向这个 `id` 的 entry 在它 Active 后启动，在它停止前先清理。同一个 `id` 同时只能有一个提供者。

## 调用与事件

类型化的调用与事件直接在 `host.Scope()` 上用生成的键和客户端（见[数据契约](contracts.md#类型化服务)）。无类型的入口：

```go
data, err := host.Call(ctx, contract.Requirement{Name: "example.counter", Major: 1}, "execute", input)
result, err := host.Emit(ctx, contract.Event{Kind: contract.KindEvent, Name: "example.changed", Input: payload, Mode: contract.Sequential})
```

`Call` 按根作用域解析 provider；`Emit` 触发事件（`Sequential`、`Parallel`）或查询（`First`，结果带显式的 `Handled`），覆盖进程内插件和所有 native 实例。`host.Caller(id)` 返回某个已登记 entry 看到的解析视图，用法同 `Call`；entry 移除后视图失效，它不是插件身份，也没有清理特权。

`host.Resolver()` 返回供[配置文件加载器](loader.md)使用的解析器：条目的 plugin 写作 `package:<id>`（默认定义）或 `package:<id>#<definition>`，即可由配置文件运行已安装的 native 包。

## 实例控制

`instance.Update(ctx, config)` 先验证配置，再持久提交并重新初始化；同一入口也用于失败后重新启动。`host.Instance(id)` 取得当前登记，被替换后旧句柄不能控制新登记。

| 操作 | 效果 |
|---|---|
| `instance.Stop(ctx)` | 临时停止，保留登记和持久启用意图 |
| `host.Resume(ctx, id)` | 恢复已临时停止的登记 |
| `host.Retry(ctx, id)` | 保持配置，重试已完成清理的 Failed 登记 |
| `host.Disable(ctx, id)` | 持久停用并移除运行树 |
| `host.Remove(ctx, id)` | 停止后删除持久条目 |
| `host.Replace(ctx, ref)` | 校验新包后替换当前运行实例 |
| `host.Uninstall(ctx, ref)` | 卸载未被使用且不阻塞启用依赖的代码 |

插件用 `sdk.CreateChild` 创建的子实例、配置文件加载器运行的条目是动态实例：它们也能经 `host.Instance("parent.child")` 取得句柄并 `Wait`、`Update`、`Stop`。`host.Instances()` 的 `Origin` 为 `persistent` 或 `dynamic`，动态项的 `Owner` 是创建它的完整父激活身份。动态实例不写入包目录，`Disable`、`Remove`、`Resume` 面向持久登记，对动态 ID 明确报错；父重建后须重新查找，旧句柄不能控制新实例。

## 关闭与恢复

退出时用独立且有限的 context 调用 `host.Close` 并处理错误。取消 Close 的等待不表示资源已释放：只有 `host.Done()` 关闭才表示清理和目录释放完成；此时即使 Close 返回了历史错误，也可以继续下一次 Host 启动。

重开目录后，`host.Restore(ctx)` 恢复已启用的条目并创建新 activation；同一个 Host 重复 Restore 不会重试 Failed，也不会撤销本次 Host 中的临时 Stop。自动恢复须按 entry 显式配置，见[有限恢复](recovery.md)；前台 CLI 可开放[本机控制](local-control.md)。应用业务数据由应用自己管理。
