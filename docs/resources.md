---
title: 资源清理
description: 用 Scope.Defer 登记资源，按逆序释放，并处理清理失败。
---

# 资源清理

每次激活拥有自己的资源。用 `s.Defer` 登记清理，框架按登记的逆序串行执行；依赖者先于 provider 释放。

```go
connection, err := openConnection()
if err != nil { return err }
s.Defer(func(context.Context) error { return connection.Close() })
s.Defer(func(ctx context.Context) error { return flush(ctx, connection) })
```

清理按逆序执行：先 flush，再关闭连接。`Defer` 返回的函数可以提前执行同一个清理，只执行一次。激活已经结束后才登记的清理会立即执行，因为它要释放的资源已经存在。

初始化失败也会清理已经登记的资源。一个清理返回错误或 panic 不会跳过后续清理，错误会聚合返回。

事件、查询和中间件的登记也归激活所有。`On`、`Handle`、`Use` 返回的 `cancel` 用于提前撤销；退役会自动移除所有登记，不必逐个 `Defer`。取消只表示不再准入新回调，已经准入的回调仍持有引用直到完成，`Stop` 和 `Close` 会等待它们；`Once` 在第一次准入时消费，回调失败也不重试，重复取消成功。

清理使用独立于业务取消的 context，并保留旧的服务绑定：消费者的清理仍可调用 provider。取消 `Stop` 或 `Close` 的等待不会取消共享清理，也不会让状态提前成为 Stopped。不合作的进程内插件无法被强制终止；native 进程则受宽限退出和进程树回收约束。

资源释放不撤销已经提交的文件、数据库或网络操作。
