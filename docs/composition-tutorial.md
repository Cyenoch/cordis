---
title: 组合实操
description: 父子定义、显式服务隔离、宿主解析视图与资源清理。
---

# 组合实操

这个完整程序演示 builtin 组合：父实例显式隔离两个服务 key，子实例等待本组 provider；宿主通过子实例的 Caller 视图调用，停用父实例时先清理子实例。父子关系本身只表达所有权，未隔离的 key 仍然共享。

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
    parent, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "parent", Package: "tutorial", Isolate: []string{base.Key(), result.Key()},
    }, contract.Definition{
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

    provider, err := host.AddBuiltin(ctx, contract.Entry{ID: "provider", Parent: "parent", Package: "tutorial"}, contract.Definition{
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
    required := contract.Requirement{Name: result.Name, Major: result.Major}
    if _, err := host.Call(ctx, required, json.RawMessage(`null`)); contract.ErrorCode(err) != contract.NotReady {
        return fmt.Errorf("root unexpectedly resolved isolated service: %v", err)
    }
    view, err := host.Caller(child.Status().Identity.Entry)
    if err != nil { return err }
    value, err := view.Call(ctx, required, json.RawMessage(`null`))
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

再创建一组不同 entry ID、相同 Isolate 声明的父子树，就能复用这些服务名称；两组只绑定各自的 provider。要共享日志服务，保持日志 key 不在 Isolate 列表中即可。嵌套子项可显式建立更近的边界，缺服务不向外回退。服务隔离不影响事件：事件仍广播到整个 Host。

同样的父子 API 可用于 native 插件：在作者契约中声明多个定义，通过 `sdk.Run` 一次提供它们；子实例从相同包版本选择定义，每个 activation 独占进程。仓库 chain 示例的 `optional-parent` 接受 `childIsolate` 配置并传给 `contract.Child.Isolate`；`TestNativeIsolationRestoreAndChildBoundaries` 实际执行两组签名 native 插件、独立更新/停用和重开恢复。完整构建方式见[快速开始](getting-started.md)。
