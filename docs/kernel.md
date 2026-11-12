---
title: 进程内内核
description: 用类型化的键在一个进程里组合插件：服务依赖、事件、中间件链、清理与配置更新。
---

# 进程内内核

根包 `github.com/Cyenoch/cordis` 是一个只依赖标准库的组合内核。插件在同一个进程里组成一棵实例树，通过类型化的键交换普通的 Go 值，登记的东西随实例停止而撤销。

本页对应可运行示例 [`examples/kernel`](https://github.com/Cyenoch/cordis/blob/main/examples/kernel/main.go)。同一个 `cordis.Plugin` 也可以用 `sdk.Define` 作为 native 包运行（[编写插件](plugin-author.md)），Host 把进程内插件与 native 实例放在同一个内核里（[接入宿主](host-guide.md)）；跨进程的服务、事件和链用 `contract` 里的键，见[数据契约](contracts.md#类型化服务)。

## 键

键是包级变量，类型写在键上：

```go
type Greeter interface {
    Greet(name string) string
}

var (
    GreeterService = cordis.NewService[Greeter]("greeter")
    Render         = cordis.NewChain[string, string]("render")
)
```

| 键 | 登记 | 执行 |
|---|---|---|
| `Service[T]` | `Provide(s, v)` | `Get(s)` |
| `Event[T]` | `On(s, fn)` | `Emit`、`EmitParallel` |
| `Query[In, Out]` | `Handle(s, fn)` | `Ask` |
| `Chain[In, Out]` | `Use(s, middleware)` | `Do` |
| `Setting[O]` | `With(s, v)` | `Values(s)` |

类型不符是编译错误。服务键的类型是接口时，可以直接发布实现它的具体类型。

## 插件

插件是一个描述结构体。`Start` 收到随实例取消的 `ctx`、只对本次激活有效的作用域和类型化的配置：

```go
var GreeterPlugin = cordis.Plugin[GreeterConfig]{
    Name: "greeter",
    Validate: func(c GreeterConfig) error {
        if c.Greeting == "" {
            return fmt.Errorf("greeting must not be empty")
        }
        return nil
    },
    Start: func(_ context.Context, s *cordis.Scope, c GreeterConfig) error {
        s.Defer(func(context.Context) error {
            fmt.Println("greeter: stopped", c.Greeting)
            return nil
        })
        return GreeterService.Provide(s, greeter{greeting: c.Greeting})
    },
}
```

在作用域上登记的一切都归属该实例：`Defer` 的清理按登记逆序执行一次，`Go` 启动的后台工作在停止时被取消并等待，发布的服务和登记的处理函数在停止时撤销。

## 依赖

`Requires` 里声明的服务齐备后 `Start` 才运行。`Get` 返回值和错误；对已声明的服务，它在实例的整个生命期（包括清理）内都成功：

```go
var ShoutPlugin = cordis.Plugin[struct{}]{
    Name:     "shout",
    Requires: []cordis.Requirement{GreeterService},
    Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
        g, err := GreeterService.Get(s)
        if err != nil {
            return err
        }
        fmt.Println("shout: started with", g.Greet("plugin"))
        Render.Use(s, func(ctx context.Context, text string, next cordis.Handler[string, string]) (string, error) {
            out, err := next(ctx, text)
            return strings.ToUpper(out), err
        })
        return nil
    },
}
```

provider 停止或重启时，依赖它的实例先完成清理，provider 之后才释放自己的资源；provider 回来后它们以新的绑定重新启动。没有声明就获取别的实例的服务会得到 `ErrNotRequired`。可选依赖用 `s.When(requires, start)`：它只在依赖齐备时运行，不让实例自己等待。

服务需要知道是谁在调用时，让方法显式接收调用方的作用域，例如 `router.Handle(s, path, h)`，并在该作用域上 `Defer`。调用来自 native 插件时，Host 在 ctx 上标出请求方，见 [Host 指南](host-guide.md#调用与事件)；进程内的调用只在沿用 native 请求的 ctx 时带着这个标记。

## 运行

```go
app := cordis.New(cordis.Options{})
defer app.Close(ctx)
root := app.Scope()

shout := ShoutPlugin.Load(root, struct{}{})
shout.Wait(ctx) // 没有 greeter，处于 Waiting

greeter := GreeterPlugin.Load(root, GreeterConfig{Greeting: "hello"})
greeter.Wait(ctx)
shout.Wait(ctx)

greeter.Update(ctx, GreeterConfig{Greeting: "goodbye"}) // greeter 重启，shout 先停后启
greeter.Update(ctx, GreeterConfig{})                    // 校验失败，实例不变
```

`Load` 立即返回，`Wait` 等到没有在途的启动或停止。运行 `go run ./examples/kernel` 的输出：

```text
shout: waiting until a greeter exists
shout: started with hello, plugin
HELLO, WORLD!
greeter: stopped hello
shout: started with goodbye, plugin
GOODBYE, WORLD!
rejected: greeting must not be empty
greeter: stopped goodbye
```

## 状态与失败

实例的状态是 Waiting、Starting、Active、Quiescing、Stopped、Failed。

- `Start` 返回错误或 panic、后台工作返回错误、配置未通过校验，都会让实例清理后进入 Failed。Failed 不会自动重试，也不因依赖变化而恢复，只有 `Update` 或 `Restart` 能恢复。
- `Status()` 给出状态、错误、激活次数，以及 Waiting 时缺少的服务与原因。
- `Stop` 是终态。停止顺序是：依赖它的实例先清理，子实例停止，然后取消它的 `ctx` 并等待在途的回调和后台工作，最后执行它自己的清理。前两步期间它的处理函数和后台工作仍然可用。
- 在某个实例的回调里等待它自己、它的祖先或它依赖的 provider 停止，会立即得到 `ErrReentrant`，而不是死锁。传入不带内核标记的 `context.Background()` 时无法检测。

处理函数可能被并发调用，需要自行保证并发安全。

## 更多能力

| 需要 | 做法 |
|---|---|
| 两组插件各用自己的同名服务 | `s.Isolate(key)` 派生作用域，在其上加载这一组；多个作用域共用一个空间用 `IsolateIn(realm, key)` |
| 调用方按作用域给服务传配置 | `Setting[O]`：`With` 附加，`Default` 写在 `Requires` 里，provider 用 `Values(callerScope)` 读取 |
| 不重启地应用新配置 | 在 `Start` 里 `cordis.OnUpdate(s, fn)`，`fn` 返回是否已就地应用 |
| 依赖带条件（例如版本） | `key.Where(accept)`；条件结果被缓存，provider 调用 `key.Changed(s)` 时重新评估 |
| 提前撤销一组登记 | `sub, release := s.Sub()` |
| 把事件限定到一棵子树 | `s.Filter(func(l *cordis.Scope) bool { return l.Within(s) })` |
| 观察实例与服务的变化 | 监听 `cordis.InstanceChanged`、`cordis.ServiceChanged` |
| 流式结果 | 让服务方法返回 `iter.Seq2[T, error]` |
| 用配置文件组装并在运行期编辑 | [配置文件加载器](loader.md) |
| 日志 | [日志服务](logging.md)：按实例命名的 `*slog.Logger` |
| 定时器 | [定时辅助](timers.md)：随实例取消的延时、周期、节流与防抖 |

与官方 Cordis 的名称对应和差异见[组合与生命周期](composition.md#与官方-cordis-的对应)。
