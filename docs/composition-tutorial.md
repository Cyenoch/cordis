---
title: 组合实操
description: 父定义直接携带子定义，等待 provider 到达，再按所有权释放资源。
---

# 组合实操

这个完整程序演示 builtin 组合：父实例先发布，子实例等待依赖；provider 到达后子实例开始提供服务；停用父实例时先清理子实例。一个 Host 共享服务表，父子关系只表达所有权。

在独立 Go module 中将 `github.com/Cyenoch/cordis` replace 到本地检出，执行 `go mod tidy` 和 `go run .`。项目尚未发布，具体设置见[快速开始](getting-started.md)。

```go title="main.go"
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "time"

    "github.com/Cyenoch/cordis"
    "github.com/Cyenoch/cordis/contract"
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
    directory, err := os.MkdirTemp("", "cordis-composition-")
    if err != nil { return err }
    defer os.RemoveAll(directory)
    host, err := cordis.Open(cordis.Options{Directory: directory})
    if err != nil { return err }
    defer func() {
        closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if err := host.Close(closing); err != nil {
            fmt.Fprintln(os.Stderr, err)
        }
    }()

    base := contract.Service{Name: "example.base", Major: 1, Minor: 0}
    result := contract.Service{Name: "example.result", Major: 1, Minor: 0}
    need := contract.Requirement{Name: base.Name, Major: base.Major}
    childReady := make(chan contract.ChildInstance, 1)
    childDefinition := contract.Definition{
        ID: "feature",
        Requires: []contract.Requirement{need},
        Provides: []contract.Service{result},
        Start: func(_ context.Context, scope contract.Scope) error {
            if err := scope.Defer(func(context.Context) error {
                fmt.Println("child stopped")
                return nil
            }); err != nil { return err }
            return scope.Provide(result, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
                return scope.Call(ctx, need, input)
            })
        },
    }
    parent, err := host.AddBuiltin(ctx, contract.Entry{ID: "parent", Package: "tutorial"}, contract.Definition{
        ID: "parent",
        Children: []contract.Definition{childDefinition},
        Start: func(ctx context.Context, scope contract.Scope) error {
            if err := scope.Defer(func(context.Context) error {
                fmt.Println("parent stopped")
                return nil
            }); err != nil { return err }
            child, err := contract.CreateChild(ctx, scope, contract.Child{ID: "optional", Definition: "feature"})
            if err != nil { return err }
            childReady <- child
            return nil
        },
    })
    if err != nil { return err }
    if err := parent.Wait(ctx); err != nil { return err }
    child := <-childReady
    fmt.Println("parent ready")
    fmt.Println("child", child.Status().State)

    provider, err := host.AddBuiltin(ctx, contract.Entry{ID: "provider", Package: "tutorial"}, contract.Definition{
        ID: "provider",
        Provides: []contract.Service{base},
        Start: func(_ context.Context, scope contract.Scope) error {
            return scope.Provide(base, func(context.Context, json.RawMessage) (json.RawMessage, error) {
                return json.RawMessage(`42`), nil
            })
        },
    })
    if err != nil { return err }
    if err := provider.Wait(ctx); err != nil { return err }
    if err := child.Wait(ctx); err != nil { return err }
    value, err := host.Call(ctx, contract.Requirement{Name: result.Name, Major: result.Major}, json.RawMessage(`null`))
    if err != nil { return err }
    fmt.Println("child value", string(value))
    return host.Disable(ctx, "parent")
}
```

```text title="预期输出"
parent ready
child waiting
child value 42
child stopped
parent stopped
```

`Definition.Children` 把可用子定义放在父旁边。CreateChild 在父发布后启动子实例，所以父初始化只登记、不等待。`Defer` 将资源绑定到当前 activation，父停用会等待子清理。

同样的父子 API 可用于 native 插件：在作者契约中声明多个定义，通过 `sdk.Run` 一次提供它们；子实例从相同包版本选择定义，每个 activation 独占进程。完整构建方式见[快速开始](getting-started.md)。
