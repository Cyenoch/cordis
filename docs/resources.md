# 显式资源组

`contract.Group` 把若干 `contract.Cleanup` 组合为一个有所有权的清理单元。它只依赖公共 `contract` 包，本地插件与 SDK 插件作者都不需要导入宿主 `internal`。

## API 与所有权

- `contract.NewGroup(owner interface{ Defer(contract.Cleanup) error }) (*contract.Group, error)` 在返回前把组的 `Close` 登记给 owner。owner 可以是当前 activation 的 `contract.Scope`，也可以是另一个 Group。登记失败返回 `nil, error`，不会交付未受管理的组。
- `var group contract.Group` 是可用的独立资源组；调用者必须自己调用 `Close`。Group 使用后不得复制。
- `group.Defer(cleanup)` 只在成功时接管该清理。nil cleanup 返回 `invalid_message`；开始关闭后拒绝新登记，返回 `not_ready`。登记失败时，刚取得的资源仍由调用者负责释放。
- `group.Close(ctx)` 开始一次共享释放并等待结束。第一次 Close 立即封闭登记；同时或以后调用 Close 不会再次释放，也不会重试失败的清理。完成的等待者取得同一个缓存的聚合错误；全部成功时为 nil。

Scope 在初始化失败、停用或退役时执行已拥有的清理，所以应先创建受 Scope 管理的组，再逐个取得资源并登记。嵌套组把自己的 Close 登记在父组中，不引入第二套 activation 或服务绑定。

```go
package plugin

import (
    "context"
    "errors"
    "os"

    "github.com/Cyenoch/cordis/contract"
)

func start(_ context.Context, scope contract.Scope) error {
    group, err := contract.NewGroup(scope)
    if err != nil {
        return err
    }
    for _, path := range []string{"input.json", "rules.json"} {
        file, err := os.Open(path)
        if err != nil {
            return err // Scope 仍会清理此前已经登记的文件。
        }
        if err := group.Defer(func(context.Context) error { return file.Close() }); err != nil {
            return errors.Join(err, file.Close()) // 登记失败，没有转移所有权。
        }
    }
    return nil
}
```

## 顺序、失败与取消

同一组严格按登记逆序**串行等待**。先登记连接，再登记使用连接的会话时，必须等会话清理真正返回后才关闭连接。即使会话清理异步等待后失败，仍会尝试连接清理；错误通过 `errors.Join` 聚合，`errors.Is` / `errors.As` 保留原错误。清理 panic 被转换为错误后继续其余清理；panic 的值本身是 error 时也保留其错误链。

每个非空组最多启动一个释放 goroutine，不会为每个等待者或每个清理另开后台任务。释放使用第一次 Close 的 `context.WithoutCancel(ctx)`：保留 context 值（包括 Scope 的清理 binding 资格），但不继承调用者的取消或 deadline。清理函数必须使用传入的 cleanup context，而不是捕获已经取消的普通工作 context。需要自身超时的清理可以在 cleanup context 上显式派生并释放自己的 timeout。

等待者取消只结束自己的等待并返回 `ctx.Err()`；不取消共享清理，也不代表资源已经释放。其他等待者继续等待，后续 Close 仍能取得最终结果。如果释放已完成，Close 优先返回缓存结果，即使该等待者的 context 已取消。

不合作、永久阻塞的进程内清理无法被 Go 强杀：该组保持未完成，较早登记的依赖资源不会被提前释放。此时只有实际被该清理阻塞的共享释放 goroutine 继续存在，增加或取消等待者不会增加释放 goroutine。清理不得同步等待包含自身的 Group.Close，也不得形成父子组所有权环，否则会等待自己完成。

跨组没有全局 LIFO。父组只在执行到子组的登记位置时等待该子组。跨插件仍以服务依赖、父子实例的退役顺序为准，参见[组合、实例与清理](composition.md)。通常让 Scope 发起关闭，才能把其清理 binding context 传给整组；若业务代码提前显式 Close，则整次共享释放使用该首次调用的 context 值，后来的 Scope Close 不会替换它。

## 清理不是业务回滚

Group 负责关闭连接、取消并等待受管理工作、撤销订阅等资源生命周期；它不撤销已经提交的文件写入、网络请求、数据库提交或其他外部副作用。初始化中途失败会释放已登记资源，但不是事务，也不提供任意业务补偿。需要回滚的业务必须自己定义事务边界和补偿策略。

真实外部 C → B → A 的资源获取、传递 cleanup context、释放响应及 shutdown 确认顺序见[生命周期验收](lifecycle-acceptance.md)；Group 单测不能替代 native/WASM 进程链证据。实际执行结果统一引用 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)。
