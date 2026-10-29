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
| `host.Disable(ctx, id)` | 持久停用并移除运行树 |
| `host.Remove(ctx, id)` | 停止后删除持久条目 |
| `host.Replace(ctx, ref)` | 校验新包后替换当前运行实例 |
| `host.Uninstall(ctx, ref)` | 卸载未被使用且不阻塞启用依赖的代码 |

## 关闭与恢复

退出时调用 `host.Close`，使用独立且有限的 context 并处理错误。取消 Close 的等待不表示资源已释放；不合作的 builtin 仍会阻塞自身依赖树。

重开目录后，调用 `host.Restore(ctx)` 恢复已启用条目。恢复不会自动重试 Failed，也不会撤销本次 Host 中的临时 Stop。应用业务数据由应用自己管理。
