---
title: 生成服务实操
description: 从一份受限 JSON 契约生成 Go client/server，在真实 Host Scope 上注册和调用。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - go.mod
  - host.go
  - service.go
  - contract
  - schema
  - cmd/cordis
---

# 生成服务实操

当服务有多个方法或会被多方使用时，把数据契约交给生成器，比手工同步请求结构和 method envelope 更方便。本例生成一个 echo 服务，在 builtin Scope 上注册，再从另一个实例调用。

生成的代码只依赖 `contract`、`schema` 和标准库；注册到外部 SDK 的方式相同，不需要把宿主实现编译进插件。字段规则和编码边界见[数据契约参考](contracts.md)。

## 建立独立项目

从 Cordis 仓库根执行：

```sh
CORDIS_ROOT="$(pwd)"
go build -o /tmp/cordis-generate ./cmd/cordis
mkdir -p /tmp/cordis-generated
cd /tmp/cordis-generated
go mod init example.com/cordis-generated
go mod edit -require=github.com/Cyenoch/cordis@v0.0.0
go mod edit -replace="github.com/Cyenoch/cordis=$CORDIS_ROOT"
mkdir echo
```

Windows 可使用任意空目录及 `cordis-generate.exe`，replace 指向本地检出的绝对路径。后续 Go 与 JSON 文件不变。

## 写契约并生成

```json title="echo.schema.json"
{
  "schema": "cordis-go.contract/v1",
  "services": [{
    "name": "example.echo",
    "major": 1,
    "minor": 0,
    "methods": [{
      "name": "echo",
      "request": {
        "type": "object",
        "properties": {"text": {"type": "string", "minLength": 1}},
        "required": ["text"]
      },
      "response": {"type": "string"}
    }]
  }]
}
```

保存后生成：

```sh
/tmp/cordis-generate generate -package echo echo.schema.json > echo/api.gen.go
gofmt -w echo/api.gen.go
```

`example.echo` 对应 `ExampleEchoClient` 和 `ExampleEchoServer`，方法类型为 `ExampleEchoEchoRequest`、`ExampleEchoEchoResponse`。生成文件不应手改；契约变化后重新生成并审查差异。

## 注册和调用

```go title="main.go"
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "strings"
    "time"

    "example.com/cordis-generated/echo"
    cordis "github.com/Cyenoch/cordis"
    "github.com/Cyenoch/cordis/contract"
)

type echoServer struct{}

func (echoServer) Echo(_ context.Context, input echo.ExampleEchoEchoRequest) (echo.ExampleEchoEchoResponse, error) {
    return echo.ExampleEchoEchoResponse(strings.ToUpper(string(input.Text))), nil
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
    directory, err := os.MkdirTemp("", "cordis-generated-")
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
    service := contract.Service{Name: "example.echo", Major: 1}
    provider, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "echo", Config: json.RawMessage(`{}`),
    }, contract.Definition{ID: "echo", Provides: []contract.Service{service},
        Start: func(_ context.Context, scope contract.Scope) error {
            return echo.RegisterExampleEchoServer(scope, echoServer{})
        },
    })
    if err != nil {
        return err
    }
    if err := provider.Wait(ctx); err != nil {
        return err
    }
    consumer, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "client", Config: json.RawMessage(`{}`),
    }, contract.Definition{ID: "client",
        Requires: []contract.Requirement{{Name: service.Name, Major: 1}},
        Start: func(ctx context.Context, scope contract.Scope) error {
            client := echo.NewExampleEchoClient(scope)
            output, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{Text: "hello"})
            if err != nil {
                return err
            }
            fmt.Println("echo:", output)
            if _, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{}); err == nil {
                return errors.New("empty text should fail schema validation")
            }
            fmt.Println("empty text rejected")
            return nil
        },
    })
    if err != nil {
        return err
    }
    return consumer.Wait(ctx)
}
```

```sh
go mod tidy
go run .
```

```text title="预期输出"
echo: HELLO
empty text rejected
```

client 在发送前校验 request，server 在分派前再次校验来自另一端的数据。这里的两次校验分别位于两个信任边界。未知字段、未知方法、缺失 input 和不满足 schema 的响应都会报错，不补默认值。

## 让宿主直接调用

生成 client 需要 `Call(ctx, requirement, input)`，而 `Host.Call` 多一个 realm 参数。要在应用入口调用，可以给固定 realm 写一个小适配器，或直接使用 `Host.Call`：

```go
output, err := host.Call(ctx, "", contract.Requirement{
    Name: "example.echo", Major: 1, MinMinor: 0, MaxMinor: 0,
}, json.RawMessage(`{"method":"echo","input":{"text":"hello"}}`))
```

响应是原始方法返回值 `"HELLO"`，不是另包一层 `{"output":...}`。应用内有 Scope 时优先使用生成 client，避免手写 envelope。

## 契约变更与版本

包 SemVer、服务 major/minor 和会话协议版本是三件事。生成 client 请求声明里的精确 minor，不根据包版本猜测服务兼容性。改变必需字段、字段含义或方法语义时，先决定服务兼容策略，再同步 consumer 的 requirement、Manifest 和生成文件。

需要可选、nullable、64 位整数、字节或 tagged union 时，使用[受限 Schema](contracts.md)明确声明。普通 Go 指针或 `interface{}` 不能代替跨进程契约。
