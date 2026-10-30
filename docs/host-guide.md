---
title: 接入宿主
description: 安装与选择 native 包，启用实例并等待清理。
---

# 接入宿主

Host 连接插件组合状态机和本地包目录。进程内定义使用 `AddBuiltin`；外部插件使用 `Install`、`Select`、`Enable`。

## 打开与启用

```go
host, err := cordis.Open(cordis.Options{
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

原生执行为默认路径，`FrameBytes` 默认 1 MiB。包目录由当前 Host 独占，直到 Close 完成。完整程序见仓库的 `examples/basic-host/main.go`。

## 调用与控制

`host.Call(ctx, requirement, input)` 和 `host.Emit(ctx, name, input, mode)` 使用当前 Host 的服务表。插件内部调用使用 Scope 绑定，不能绕过自身声明的依赖。

`instance.Update(ctx, config)` 先验证配置，再持久提交和重新初始化；同一入口也用于失败后重新启动。通过 `host.Instance(id)` 重新取得被替换的实例，旧句柄不能控制新登记。

| 操作 | 效果 |
|---|---|
| `instance.Stop(ctx)` | 临时停止，保留登记和持久启用意图 |
| `host.Resume(ctx, id)` | 恢复已临时停止的登记 |
| `host.Retry(ctx, id)` | 保持配置，重试当前已完成清理的 Failed 登记 |
| `host.Disable(ctx, id)` | 持久停用并移除运行树 |
| `host.Remove(ctx, id)` | 停止后删除持久条目 |
| `host.Replace(ctx, ref)` | 校验新包后替换当前运行实例 |
| `host.Uninstall(ctx, ref)` | 卸载未被使用且不阻塞启用依赖的代码 |

动态子实例也能通过 `host.Instance("parent.child")` 取得句柄并调用 Wait、Update、Stop；`Instances()` 的 Origin/Owner 区分来源和所属父激活。Update 不生成持久配置，Stop 只停止目标子树。Disable/Remove/Resume 面向静态登记，对动态 ID 明确报错；需要恢复动态子树时重启其所有者。父重建后须重新查找，旧句柄不能控制新 child。

## 关闭与恢复

退出时调用 `host.Close`，使用独立且有限的 context 并处理错误。取消 Close 的等待不表示资源已释放；不合作的 builtin 仍会阻塞自身依赖树。只有 `host.Done()` 已关闭才表示清理及目录释放完成；此时即使 Close 有错误，也可以继续下一次宿主启动。

重开目录后，调用 `host.Restore(ctx)` 恢复已启用条目并创建新 activation。同一个 Host 重复 Restore 不自动重试 Failed，也不会撤销本次 Host 中的临时 Stop；这不代表跨重启的失败隔离。应用业务数据由应用自己管理。

需要自动恢复时，按已保存的 native entry 显式配置 `Options.Recovery`，见[有限故障恢复](recovery.md)。恢复循环随 Host 关闭，只有调度退出、激活排空且目录释放后 Done 才结束。前台 CLI 可选择开放[本机控制](local-control.md)。
