---
title: 组合实操
description: 父子实例、服务隔离、可选依赖与清理顺序的完整程序。
---

# 组合实操

一个完整程序演示实例如何组合：一组插件隔离两个服务，父实例加载的子实例等待本组的 provider，根作用域看不到组内的服务，停止父实例时子实例先清理。父子关系只表达归属，没有隔离的服务仍然共享。

在独立 Go module 中把 `github.com/Cyenoch/cordis` replace 到本地检出（设置见[快速开始](getting-started.md)），执行 `go mod tidy` 和 `go run .`。

```go title="main.go"
package main

import (
    "context"
    "errors"
    "fmt"
    "os"
    "time"

    "github.com/Cyenoch/cordis"
)

var (
    Base   = cordis.NewService[int]("example.base")
    Result = cordis.NewService[int]("example.result")
)

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run() error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    app := cordis.New(cordis.Options{})
    defer app.Close(ctx)

    // 加载在 group 上的插件有自己的 Base 与 Result，其余服务仍与外面共享。
    group := app.Scope().Isolate(Base, Result)

    feature := cordis.Plugin[struct{}]{
        Name:     "feature",
        Requires: []cordis.Requirement{Base},
        Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
            s.Defer(func(context.Context) error {
                fmt.Println("child stopped")
                return nil
            })
            base, err := Base.Get(s)
            if err != nil { return err }
            return Result.Provide(s, base)
        },
    }
    var child *cordis.Instance[struct{}]
    parent := cordis.Plugin[struct{}]{
        Name: "parent",
        Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
            s.Defer(func(context.Context) error {
                fmt.Println("parent stopped")
                return nil
            })
            // 只加载，不等待：子实例缺依赖时父照常启动。
            child = feature.Load(s, struct{}{})
            return nil
        },
    }.Load(group, struct{}{})
    if err := parent.Wait(ctx); err != nil { return err }
    fmt.Println("parent ready")
    fmt.Println("child", child.Status().State)

    provider := cordis.Plugin[struct{}]{
        Name: "provider",
        Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
            return Base.Provide(s, 42)
        },
    }.Load(group, struct{}{})
    if err := provider.Wait(ctx); err != nil { return err }
    if err := child.Wait(ctx); err != nil { return err }

    if _, err := Result.Get(app.Scope()); !errors.Is(err, cordis.ErrUnavailable) {
        return fmt.Errorf("root unexpectedly resolved an isolated service: %v", err)
    }
    value, err := Result.Get(group)
    if err != nil { return err }
    fmt.Println("child value", value)
    return parent.Stop(ctx)
}
```

```text title="预期输出"
parent ready
child waiting
child value 42
child stopped
parent stopped
```

在父的作用域上 `Load` 的插件是它的子实例：父停止时子实例先清理，`Defer` 的清理按登记的逆序执行。子实例缺依赖只会让它自己等待，不阻塞父启动。

再用一个新的 `Isolate` 作用域加载同样的插件，就得到第二组：两组各自绑定本组的 provider。要让两组共用某个服务（例如日志），不把它写进 `Isolate` 即可；多个作用域共用一个空间用 `IsolateIn`。隔离只影响服务的解析，不影响事件的传播；要把事件限定在一棵子树里，用 `Scope.Filter`。

native 插件使用同一套 API：`start` 收到的也是 `*cordis.Scope`，在其中 `Load` 的子插件与它运行在同一个进程里。需要 Host 管理的同包子实例时，用 `sdk.CreateChild` 申请同包的另一个定义；entry 与 `contract.Child` 的 `Isolate` 声明对应这里的 `Scope.Isolate`，见[编写插件](plugin-author.md#子实例)。
