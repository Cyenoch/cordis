---
title: 编写插件
description: 写一个 native 插件：入口、依赖、事件、子实例、并发与测试。
---

# 编写插件

native 插件是独立 Go module 里的一个进程：它声明提供和依赖的服务，由 Host 以签名包的形式启动，并通过会话与 Host 及其他插件通信。本页写插件代码；创建项目和第一次运行见[快速开始](getting-started.md)。

## 最小插件

作者维护 `plugin.toml` 和业务 Go。服务、请求、响应和配置是 Go 类型，用 `//cordis:` 指令声明，`cordis generate` 推导契约并生成绑定（[数据契约](contracts.md#用-go-类型写契约)）。生成的 `CounterPlugin` 是 `cordis.Plugin[Config]` 的值，`sdk.Define` 把它变成可运行的定义：

```go
// Counter 是插件提供的服务。
//
//cordis:service example.counter 1
type Counter interface {
    Execute(ctx context.Context, request Request) (Response, error)
}

type Config struct {
    Initial int64 `json:"initial,omitempty"`
}

//cordis:plugin counter
//cordis:provides Counter
func start(ctx context.Context, s *cordis.Scope, config Config) error {
    c := &counter{}
    c.value.Store(config.Initial)
    if _, err := changed.On(ctx, s, func(context.Context, struct{}) error {
        c.events.Add(1)
        return nil
    }); err != nil {
        return err
    }
    return ProvideCounter(s, c)
}

func main() {
    if err := sdk.Run(context.Background(), sdk.Define(CounterPlugin)); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

`start` 收到的 `*cordis.Scope` 与[进程内内核](kernel.md)里的是同一个类型：插件进程里运行着一个真正的内核，`Defer`、`Go`、`Load`、`Isolate` 等都可以使用。`sdk.Run` 接受多个定义，Host 在初始化时选择其中一个；每次激活独占一个进程。stdout 只传协议，日志写 stderr；`Run` 返回清理或运行错误，由 `main` 决定退出方式。

## 依赖

用 `//cordis:requires` 声明依赖，Host 在所有依赖就绪后才启动进程，并把绑定的服务交给 `start`：

```go
//cordis:plugin main
//cordis:requires Message
func start(ctx context.Context, s *cordis.Scope, config Config) error {
    client, err := GetMessage(s)
    if err != nil { return err }
    ...
}
```

`Message` 可以是导入包里的接口，也可以是本包声明的、只含自己调用的方法的接口，Host 按方法指纹匹配（示例见 `examples/plugins/consumer`）。

调用另一个进程里的服务与调用本进程的服务写法一致，经由 Host 路由，数据按契约校验。`Start` 内可以发出这类请求，也可以登记事件、查询和中间件；这些请求以初始化为父，在初始化成功后才对外生效。

## 事件、查询与中间件

跨进程的事件、查询和中间件链用 `contract` 里的键，键定义在提供方和使用方共享的包里：

```go
var (
    changed = contract.NewEventKey[struct{}]("example.changed")
    Render  = contract.NewChainKey[string, string]("example.render")
)

cancel, err := changed.On(ctx, s, handler)               // 监听；返回 cancel 可提前撤销
err = changed.Emit(ctx, s, struct{}{})                   // 触发，按登记顺序，遇错停止
_, err = Render.Use(ctx, s, func(ctx context.Context, in string, next func(context.Context, string) (string, error)) (string, error) {
    return next(ctx, "<" + in + ">")
})
```

`Once`、`Prepend` 登记选项、`Emit`/`EmitParallel`、`Ask`/`Handle` 与进程内键的语义相同，登记归属当前激活，退役时自动撤销并等待已准入的回调，通常不必再 `Defer`。事件的作用范围是整个 Host，包括进程内插件和所有 native 实例；每次激活最多 256 个活动登记。

链的每个跨进程中间件需要两次往返，一次执行的耗时等于最慢的一步，且执行本身没有期限：给 `Do` 的 context 设置期限。链的每一跳占用会话的一个在途请求直到整次执行返回，没有空位时该跳以 `not_ready` 失败。

## 清理

用 `s.Defer` 登记清理，按登记逆序执行，初始化失败时已登记的也会执行；清理不是业务事务回滚。

```go
resource, err := openResource()
if err != nil { return err }
s.Defer(func(context.Context) error { return resource.Close() })
```

停止 provider 时，Host 先让所有消费者完成清理，再结束 provider 的进程，所以消费者的清理仍然可以调用 provider。不要缓存跨激活的 `Scope`。

## 子实例

`sdk.CreateChild(ctx, s, contract.Child{ID: "optional", Definition: "feature"})` 向 Host 登记同包的另一个定义，作为本激活的子实例运行（独立进程）。它先登记，父发布后才启动；返回的句柄可 `Wait`、`Update`、`Stop`。不要在父 `Start` 内等待子实例启动；子实例缺依赖只让它自己等待，不阻塞父和兄弟。`Child.Isolate` 为子树建立服务边界，见[组合](composition.md#按服务隔离)。父重新启动后，旧的 `Scope` 不能再创建子实例。

## 并发回调

服务方法、事件监听、查询处理者和链中间件都可能并发调用：Host 对一个插件最多并行 56 个业务请求，更多的排队，之间没有顺序保证。实现要自己保护共享状态。

- 需要随激活取消、退役时等待的后台工作，用 `s.Go(func(ctx context.Context) error)`：在 `Start` 内登记的工作等实例 Active 后才开始，返回非取消错误会使实例失败。`Start` 与 `Go` 收到的 `ctx` 都在实例停止时取消。
- 退役先取消 context、等待已准入的回调和 `Go` 工作返回，再运行 `Defer` 清理；回调应当尊重 `ctx`。
- 流方法在被调用方期限内必须有产出：调用方在被调方有额度却一个期限（默认 30 秒）内没有收到任何一项时，以 `timeout` 结束。长时间空闲的流应定期发送心跳项。
- `Start` 内不要消费流：流在初始化完成后才开始，`Start` 等它会等到初始化超时。把消费放进 `s.Go`。

## 流与中间件链的开销

- 流有背压：调用方每次最多缓冲一个窗口（16 项），消费掉一半才追加额度，生产方在额度用尽时阻塞在 `yield` 上。
- 一个流从开始到结束占用会话的一个在途名额。名额数由宿主决定（默认 64，其中 8 个留给会话自身）；需要大量并发的长流时，合并或分页，或请宿主调高 `Inflight`。
- 流的期限只约束“生产方可以发送却没有发送”的时间，调用方自己消费得慢不会超时；整个流的时长由调用方的 `ctx` 决定。
- 中间件链每经过一个别的进程的中间件，多两次往返（调用它一次，它调用 `next` 一次），并在这次执行返回之前一直占用该会话的一个请求。会话没有空闲请求时，这一跳以 not-ready 失败，不排队。
- 链的执行没有自己的期限：`Do` 的调用方应传入带期限的 `ctx`，取消会传到链上正在执行的每个进程。
- 中间件里 `next` 至多调用一次，且必须在中间件返回之前调用。

## native 运行与进程内运行的差异

同一个 `cordis.Plugin` 既可以 `sdk.Define` 成 native 包，也可以在 Host 应用里 `Load` 到 `host.Scope()` 上。差别来自进程边界：

| | 进程内 | native |
|---|---|---|
| 服务、事件、查询、链 | `cordis.Service`、`Event`、`Query`、`Chain`，传递任意 Go 值 | `contract.EndpointKey`、`EventKey`、`QueryKey`、`ChainKey`，传递校验过的 JSON；`cordis` 的键只在插件进程内可见 |
| 失败隔离 | 回调的 panic 被内核捕获并作为该实例的错误 | 进程崩溃或非请求退出使该 entry Failed，不影响其他进程 |
| 调用期限 | 由调用方 context 决定 | 默认 30 秒，上限 120 秒；链不设限 |
| 登记事件、查询、链 | 同步、不返回网络错误 | 往返 Host，返回 error，初始化期间由 Host 确认 |
| 权限 | 应用进程的权限 | 启动用户的权限，不是沙箱 |

## 开发循环

在作者目录完成 `go mod tidy` 后：

```sh
cordis dev -dev-unsigned -go-package . -config-file ../development.toml .
```

保存 Go、资源或配置文件会重新生成、构建并重启；模块外的依赖用 `-watch` 显式加入。编译或配置失败时保留旧实例，修复后自动重建。同时开发 provider 和 consumer 等多个本地包时用 `-project FILE`，见[多插件联合开发](multi-package-dev.md)；参数与边界见 [CLI](cli.md#编辑后自动重启)。

## 测试

`plugintest.Start` 用真实的 Host、真实的 SDK 初始化、生成的 codec 和清理运行一个定义，不需要进程：

```go
h, err := plugintest.Start(t.Context(), CounterDefinitionSpec(), sdk.Define(CounterPlugin),
    plugintest.Options{Config: json.RawMessage(`{"initial":10}`)})
if err != nil { t.Fatal(err) }
defer h.Close(t.Context())

client, err := GetCounter(h.Scope())
```

`Options.Setup` 在插件启动前往根作用域加载提供依赖的进程内插件，`Options.Children` 提供同包的其他定义；`h.Scope()` 是 Host 的根作用域，在它上面触发事件、调用服务即可观察插件。进程、信号和安装行为由 native 集成测试验证。

[数据契约](contracts.md) · [发布插件](plugin-release.md) · [资源清理](resources.md) · [native 运行时](runtimes.md)
