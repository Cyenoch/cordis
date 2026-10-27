---
title: 组合实操
description: 在一个完整程序里创建可选子实例、等待服务、隔离 realm，并使用事件、中间件和资源组。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - go.mod
  - host.go
  - children.go
  - instance.go
  - service.go
  - ownership.go
  - contract
  - internal/core
---

# 组合实操

这个程序创建一个父实例和一个可选子实例。父实例先就绪，子实例等待迟到的服务；服务出现后，子实例可以处理请求。最后停用父实例，观察子实例先释放资源。

示例使用进程内 builtin，便于直接查看生命周期。外部插件使用相同的 Scope 和 Child API，但要用 `sdk.ServeDefinitions` 提供定义，并在 Manifest 中声明它们；每个外部 activation 仍是独立进程。真实外部链见[生命周期验收](lifecycle-acceptance.md)。

## 准备 module

以下实操用绝对 `replace` 指向页尾代码基线所对应的检出，方便与当前源码一起验证。从 Cordis 仓库根执行：

```sh
CORDIS_ROOT="$(pwd)"
mkdir -p /tmp/cordis-composition
cd /tmp/cordis-composition
go mod init example.com/cordis-composition
go mod edit -require=github.com/Cyenoch/cordis@v0.0.0
go mod edit -replace="github.com/Cyenoch/cordis=$CORDIS_ROOT"
```

Windows 可新建一个空目录，用 `go mod edit -replace=github.com/Cyenoch/cordis=C:/path/to/cordis` 指向检出。先保存程序，再执行 `go mod tidy`。

## 完整程序

```go title="main.go"
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "time"

    cordis "github.com/Cyenoch/cordis"
    "github.com/Cyenoch/cordis/contract"
)

type Input struct {
    Delta int `json:"delta"`
}

type Output struct {
    Value int `json:"value"`
}

type Question struct {
    Text string `json:"text"`
}

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run() (result error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    directory, err := os.MkdirTemp("", "cordis-composition-")
    if err != nil {
        return err
    }
    defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
    host, err := cordis.Open(cordis.Options{Directory: directory})
    if err != nil {
        return err
    }
    defer func() {
        closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
        defer stop()
        result = errors.Join(result, host.Close(closeCtx))
    }()

    base := contract.Service{Name: "tutorial.base", Major: 1}
    doubled := contract.Service{Name: "tutorial.double", Major: 1}
    required := contract.Requirement{Name: base.Name, Major: 1}
    if err := host.ConfigureRealm("demo", "", []string{base.Key(), doubled.Key()}); err != nil {
        return err
    }
    childReady := make(chan contract.ChildInstance, 1)
    released := make(chan string, 3)
    if err := host.RegisterDefinition(ctx, "tutorial", contract.Definition{
        ID: "feature", Requires: []contract.Requirement{required},
        Provides: []contract.Service{doubled},
        Start: func(_ context.Context, scope contract.Scope) error {
            group, err := contract.NewGroup(scope)
            if err != nil {
                return err
            }
            for _, name := range []string{"child-first", "child-last"} {
                if err := group.Defer(func(context.Context) error {
                    released <- name
                    return nil
                }); err != nil {
                    return err
                }
            }
            final := func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
                response, err := scope.Call(ctx, required, input)
                if err != nil {
                    return nil, err
                }
                var output Output
                if err := contract.DecodeData(response, &output); err != nil {
                    return nil, err
                }
                output.Value *= 2
                return contract.Encode(output)
            }
            nonNegative := func(ctx context.Context, input json.RawMessage, next contract.Handler) (json.RawMessage, error) {
                var request Input
                if err := contract.DecodeData(input, &request); err != nil {
                    return nil, err
                }
                if request.Delta < 0 {
                    return nil, contract.Fail(contract.Invalid, "delta must be non-negative")
                }
                return next(ctx, input)
            }
            return scope.Provide(doubled, contract.Chain(final, nonNegative))
        },
    }); err != nil {
        return err
    }
    question := contract.EventKey[Question]{Name: "tutorial.question", Mode: contract.First}
    parent, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "parent", Package: "tutorial", Definition: "parent", Realm: "demo",
        Config: json.RawMessage(`{}`),
    }, contract.Definition{ID: "parent", Start: func(ctx context.Context, scope contract.Scope) error {
        if err := scope.Defer(func(context.Context) error {
            released <- "parent"
            return nil
        }); err != nil {
            return err
        }
        child, err := contract.CreateChild(ctx, scope, contract.Child{
            ID: "optional", Definition: "feature", Config: json.RawMessage(`{}`),
        })
        if err != nil {
            return err
        }
        childReady <- child
        if err := contract.Subscribe(scope, question, func(context.Context, Question) (contract.EventResult, error) {
            return contract.EventResult{Handled: true, Value: json.RawMessage(`false`)}, nil
        }); err != nil {
            return err
        }
        return contract.Subscribe(scope, question, func(context.Context, Question) (contract.EventResult, error) {
            return contract.EventResult{}, errors.New("First should have stopped")
        })
    }})
    if err != nil {
        return err
    }
    if err := parent.Wait(ctx); err != nil {
        return err
    }
    child := <-childReady
    fmt.Println("parent ready before provider")

    provider, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "provider", Package: "tutorial", Realm: "demo", Config: json.RawMessage(`{}`),
    }, contract.Definition{ID: "provider", Provides: []contract.Service{base},
        Start: func(_ context.Context, scope contract.Scope) error {
            return cordis.Provide(scope, cordis.Service[Input, Output]{Contract: base},
                func(_ context.Context, input Input) (Output, error) {
                    return Output{Value: 10 + input.Delta}, nil
                })
        },
    })
    if err != nil {
        return err
    }
    if err := provider.Wait(ctx); err != nil {
        return err
    }
    if err := child.Wait(ctx); err != nil {
        return err
    }
    response, err := host.Call(ctx, "demo", contract.Requirement{
        Name: doubled.Name, Major: 1,
    }, json.RawMessage(`{"delta":2}`))
    if err != nil {
        return err
    }
    fmt.Println("call:", string(response))
    events, err := host.Emit(ctx, "demo", question.Name, json.RawMessage(`{"text":"continue?"}`), question.Mode)
    if err != nil {
        return err
    }
    if len(events) != 1 || !events[0].Handled {
        return errors.New("expected one handled event")
    }
    fmt.Println("event:", string(events[0].Value))
    if err := host.Disable(ctx, "parent"); err != nil {
        return err
    }
    fmt.Printf("cleanup: %s, %s, %s\n", <-released, <-released, <-released)
    return nil
}
```

## 运行与结果

```sh
go mod tidy
go run .
```

```text title="预期输出"
parent ready before provider
call: {"value":24}
event: false
cleanup: child-last, child-first, parent
```

父实例的 `Start` 只登记子实例，没有调用 `child.Wait`。它必须先返回并完成发布，子实例才能启动。父初始化中等待自己的子树会返回 `reentrant_call`，不能用 goroutine 绕过。

`demo` realm 隔离了两个服务 key，子实例继承父 realm。调用使用 `host.Call(ctx, "demo", ...)`；同一个服务要启用多份时，为每份实例创建独立 realm。realm 控制服务解析，不是文件或网络权限。

事件采用 `First`：第一个监听者明确返回 `Handled:true`，即使值是 `false` 也会停止。通知所有监听者时改用 `Sequential` 或 `Parallel`；后者最多同时调度八个监听者，同一 activation 的回调仍串行。事件传播范围见[组合参考](composition.md)。

middleware 在下游调用前拒绝负数，也可以检查调用结果。一次调用最多执行一次 `next`，不能在失败后再次调用它实现透明重试。

清理有两层顺序：先子后父；子实例内部的 Group 按登记逆序释放。这里的 channel 只是让输出顺序可见，实际资源应在取得后立即登记，并处理登记失败时的释放。资源组不会撤销已完成的业务写入，见[资源组参考](resources.md)。

## 接到外部插件

把两个定义交给 `sdk.ServeDefinitions(ctx, parentDefinition, featureDefinition)`，Manifest 中也声明 `parent`、`feature` 及其 requires/provides。`contract.CreateChild` 不变，外部子实例只能选同包同版本的定义。动态子实例随父 activation 结束，不写成持久启用项；需要重启恢复的独立配置使用宿主持有的 `Entry.Parent`。

更新或停止子实例时使用返回的 ChildInstance；远程 `Status()` 是上次应答快照，需要新状态时调用 `Refresh(ctx)`。父停止会结束整棵子树，不必自行复制一套子实例清理逻辑。
