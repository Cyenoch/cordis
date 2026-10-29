---
title: 资源清理
description: 使用 Scope.Defer 登记、逆序释放并处理失败。
---

# 资源清理

每次 activation 拥有独立资源。使用 `scope.Defer` 登记清理，框架按登记逆序串行执行；依赖者先于 provider 释放。

```go
connection, err := openConnection()
if err != nil { return err }
if _, err := contract.Own(scope, connection); err != nil { return err }
return scope.Defer(func(ctx context.Context) error {
    return flush(ctx, connection)
})
```

这里先 flush，再关闭 connection。`Own` 接管 `Close() error` 资源；如果登记失败，会立即尝试关闭它。普通 Defer 登记失败时，资源仍由调用者负责。

初始化失败也会清理已经登记的资源。一个清理返回错误或 panic 不会跳过后续清理，错误会聚合返回。重复关闭不会重复释放。

事件订阅也归 activation 所有。`scope.On(ctx, name, options, handler)` 返回的句柄用于提前 Cancel；退役会自动移除所有监听者，不必逐个 Defer。Cancel 成功只表示不再准入新回调，已经准入的回调仍持有引用直到完成；Stop/Close 会继续等待这些回调。once 在第一次准入时消费，即使回调失败也不重试，重复 Cancel 成功。

清理使用独立于业务取消的 context，并保留旧服务绑定。取消 Stop 或 Close 的等待不会取消共享清理，也不会让状态提前成为 Stopped。不合作的 builtin 无法强制终止；native 进程则受宽限退出与进程树回收约束。

资源释放不撤销已经提交的文件、数据库或网络操作。
