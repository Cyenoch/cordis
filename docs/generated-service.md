---
title: 生成服务实操
description: 从 Go 类型生成服务端点与客户端，在内核作用域上发布和调用；声明式契约是第二种写法。
---

# 生成服务实操

`cordis generate` 从 Go 类型推导契约，并生成服务端点、客户端和插件值。本页从声明到一次真实调用走完全程；类型映射与约束规则见[数据契约](contracts.md)。

## 从 Go 类型生成

在独立 module（go.mod 的 replace 设置见[快速开始](getting-started.md#初始化独立-module)）里放下面的 `echo.go`：服务接口、请求、响应、配置，以及提供方和消费方两个插件。

```go title="echo.go"
package main

import (
    "context"
    "errors"
    "fmt"
    "iter"
    "os"
    "strings"
    "time"

    "github.com/Cyenoch/cordis"
)

// Echo 重复收到的文字。
//
//cordis:service example.echo 1.1
type Echo interface {
    Echo(ctx context.Context, request EchoRequest) (EchoResponse, error)
    Repeat(ctx context.Context, request RepeatRequest) iter.Seq2[EchoResponse, error]
}

type EchoRequest struct {
    Text string `json:"text" cordis:"minLength=1"`
}

type EchoResponse struct {
    Text string `json:"text"`
}

type RepeatRequest struct {
    Text  string `json:"text" cordis:"minLength=1"`
    Count int    `json:"count" cordis:"minimum=1,maximum=5"`
}

type Config struct {
    Prefix string `json:"prefix,omitempty"`
}

type echoServer struct{ prefix string }

func (s echoServer) Echo(_ context.Context, request EchoRequest) (EchoResponse, error) {
    return EchoResponse{Text: s.prefix + strings.ToUpper(request.Text)}, nil
}

func (s echoServer) Repeat(_ context.Context, request RepeatRequest) iter.Seq2[EchoResponse, error] {
    return func(yield func(EchoResponse, error) bool) {
        for range request.Count {
            if !yield(EchoResponse{Text: s.prefix + request.Text}, nil) {
                return
            }
        }
    }
}

//cordis:plugin echo
//cordis:provides Echo
func startEcho(_ context.Context, s *cordis.Scope, config Config) error {
    return ProvideEcho(s, echoServer{prefix: config.Prefix})
}

//cordis:plugin client
//cordis:requires Echo 1-3
func startClient(ctx context.Context, s *cordis.Scope, _ struct{}) error {
    client, err := GetEcho(s)
    if err != nil {
        return err
    }
    output, err := client.Echo(ctx, EchoRequest{Text: "hello"})
    if err != nil {
        return err
    }
    fmt.Println("echo:", output.Text)
    for line, err := range client.Repeat(ctx, RepeatRequest{Text: "hi", Count: 2}) {
        if err != nil {
            return err
        }
        fmt.Println("repeat:", line.Text)
    }
    if _, err := client.Echo(ctx, EchoRequest{}); err == nil {
        return errors.New("empty text should fail validation")
    }
    fmt.Println("empty text rejected")
    return nil
}

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
    root := app.Scope()
    if err := EchoPlugin.Load(root, Config{Prefix: "> "}).Wait(ctx); err != nil {
        return err
    }
    return ClientPlugin.Load(root, struct{}{}).Wait(ctx)
}
```

在 module 目录运行：

```sh
go mod tidy
"$CORDIS" generate
go run .
```

```text
echo: > HELLO
repeat: > hi
repeat: > hi
empty text rejected
```

`generate` 写出 `cordis_gen.go` 与 `cordis_gen.json`。`echo.go` 没有重复声明任何类型：请求和响应原样用在生成的端点和客户端里。

### 生成了什么

每个服务 `X`（接口名）生成：

| 名称 | 作用 |
| --- | --- |
| `ProvideX(s, impl)` | 在作用域 `s` 上发布实现 |
| `GetX(s)` | 取得类型化客户端，提供方在本进程或另一个进程都一样 |
| `XEndpoint` | 发布与依赖共用的端点键 |
| `XRequirement(min, max)` | 内核依赖，范围外的 provider 使消费者保持 Waiting |
| `XService()`、`XConsumer(min, max)` | 服务名与 minor、带方法指纹的消费者描述 |
| `NewXEndpoint(impl)`、`NewXClient(endpoint)` | 端点适配器与客户端；`NewXClient` 检查服务名和 major，不匹配返回错误 |

每个插件 id `p`（导出形式 `P`，`echo` 为 `Echo`）生成 `PPlugin` 和 `PDefinitionSpec()`。`PPlugin` 是 `cordis.Plugin[Config]` 的值，进程内用 `Load` 加载，native 入口用 `sdk.Define(PPlugin)`；`PDefinitionSpec()` 是 Manifest 的 definition 描述。

客户端在发送前校验请求，端点在分派前校验收到的数据，这是两个信任边界，都不补默认值；同进程调用也一样。未知方法、坏响应及失败的业务调用均明确返回错误。

### 流式方法

返回 `iter.Seq2[R, error]` 的方法是流式方法，`Repeat` 即是：

- 提供方返回一个迭代器并在其中 `yield` 各项；`yield` 返回 false 表示调用方已停止，应当立即返回。
- 调用方 `for item, err := range client.Repeat(ctx, request)`；`err` 非空是最后一次迭代。请求在开始迭代时才发出。
- 同一进程内客户端直接返回实现方的迭代器，输入照常校验，结果与跨进程时相同。
- 声明了流式方法的包，manifest 会把 `streams` 列为必需的会话特性。

完整的插件见 [`examples/plugins/feed`](https://github.com/Cyenoch/cordis/blob/main/examples/plugins/feed/main.go)；跨进程时的窗口、额度和期限见[流的规则](protocol.md#流)。

### 消费者范围

`//cordis:requires Echo 1-3` 声明接受 minor 1 到 3 的 provider，不写范围时只接受 `Echo` 声明的 minor。范围进入 `ClientDefinitionSpec()` 的 `Requires` 和 `ClientPlugin` 的依赖：客户端不会把范围缩窄成生成时的 1.1，实际 provider 为 1.2 时仍可调用。服务版本、包 SemVer 与会话协议版本相互独立。

端点在内核里就是一个普通服务：进程内的消费者、Host 应用的 `host.Scope()` 和另一个进程里的插件，都用 `GetEcho(scope)` 取得它。消费方可以导入提供方包里的接口（`//cordis:requires api.Echo`），也可以在本包声明自己用到的那部分方法，Host 按方法指纹匹配。

## 从声明式契约生成

Go 类型表达不了的形状（标记联合）和与非 Go 实现共享的契约，手写契约文件并生成 Go 类型。下面的完整项目使用公开库 `schema.GenerateMany`；命令行为 `cordis generate -output`。

### 建立独立项目

```sh
mkdir -p /tmp/cordis-generated/echo
cd /tmp/cordis-generated
go mod init example.com/cordis-generated
go get github.com/Cyenoch/cordis@latest
```

Windows 使用一个空目录。

### 写契约并生成

```json title="echo.schema.json"
{
  "schema": "cordis-go.contract/v1",
  "types": {
    "Settings": {
      "type": "object",
      "properties": {"prefix": {"type": "string"}},
      "required": ["prefix"]
    }
  },
  "services": [{
    "name": "example.echo",
    "version": "1.1",
    "methods": [{
      "name": "echo",
      "request": {
        "type": "object",
        "properties": {"text": {"type": "string", "minLength": 1}},
        "required": ["text"]
      },
      "response": {
        "type": "object",
        "properties": {"text": {"type": "string"}},
        "required": ["text"]
      }
    }]
  }],
  "definitions": [
    {"id":"echo","config":{"$ref":"#/types/Settings"},"provides":["example.echo"]},
    {"id":"client","requires":[{"name":"source","service":"example.echo","minMinor":1,"maxMinor":3}]}
  ]
}
```

`version:1` 是 1.0；`"1.1"` 是 1.1，不接受浮点数 `1.1`、补丁位或 `major`/`minor` 字段。consumer 范围必须显式声明；引用同文档服务确定 major 与方法契约，不由 provider 版本猜范围。

用公开库生成，并同时导出配置编辑器 schema：

```go title="generate.go"
//go:build ignore

package main

import (
    "bytes"
    "encoding/json"
    "os"

    "github.com/Cyenoch/cordis/schema"
)

func main() {
    if err := run(); err != nil { panic(err) }
}

func run() error {
    raw, err := os.ReadFile("echo.schema.json")
    if err != nil { return err }
    declaration, err := schema.Parse(raw)
    if err != nil { return err }
    var output bytes.Buffer
    if err := schema.GenerateMany(&output, "echo", declaration); err != nil { return err }
    if err := os.WriteFile("echo/api.gen.go", output.Bytes(), 0600); err != nil { return err }
    specs, err := declaration.Specs()
    if err != nil { return err }
    // Specs 按 ID 排序；本例 client、echo 的顺序固定。
    standard, err := schema.Export(specs[1].ConfigSchema)
    if err != nil { return err }
    if err := os.WriteFile("echo.editor.schema.json", standard, 0600); err != nil { return err }
    metadata, err := json.Marshal(specs)
    if err != nil { return err }
    return os.WriteFile("definitions.json", metadata, 0600)
}
```

```sh
go run generate.go
```

生成前统一检查符号并运行 gofmt；失败不会写半份生成内容。这里的 `os.WriteFile` 是教程的本地写文件步骤，不提供原子替换保证。多个声明可一起传给 `GenerateMany`；独立调用 `Generate` 的文件也可放在同包。指纹常量按 definition/service 命名，分次调用 `GenerateMany`（包括 CLI generate）也不会引入固定公共常量；完整声明可通过 `declaration.Fingerprint()` 取得。

### 注册和调用

```go title="main.go"
package main

import (
    "context"
    "errors"
    "fmt"
    "os"
    "strings"
    "time"

    "example.com/cordis-generated/echo"
    "github.com/Cyenoch/cordis"
)

type echoServer struct { prefix string }

func (s echoServer) Echo(_ context.Context, input echo.ExampleEchoEchoRequest) (echo.ExampleEchoEchoResponse, error) {
    return echo.ExampleEchoEchoResponse{Text: s.prefix + strings.ToUpper(input.Text)}, nil
}

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
    root := app.Scope()

    provider := echo.EchoPlugin(func(_ context.Context, s *cordis.Scope, cfg echo.EchoConfig) error {
        return echo.ProvideExampleEcho(s, echoServer{prefix: cfg.Prefix})
    }).Load(root, echo.EchoConfig{Prefix: "> "})
    if err := provider.Wait(ctx); err != nil { return err }

    consumer := echo.ClientPlugin(func(ctx context.Context, s *cordis.Scope, _ echo.ClientConfig) error {
        client, err := echo.GetExampleEcho(s)
        if err != nil { return err }
        output, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{Text: "hello"})
        if err != nil { return err }
        fmt.Println("echo:", output.Text)
        if _, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{}); err == nil {
            return errors.New("empty text should fail schema validation")
        }
        fmt.Println("empty text rejected")
        return nil
    }).Load(root, echo.ClientConfig{})
    return consumer.Wait(ctx)
}
```

```sh
go mod tidy
go run .
```

```text title="预期输出"
echo: > HELLO
empty text rejected
```

本例作者不重复手写 Definition ID、Requires/Provides、配置 Decode 或字段 schema。用于机器包时，将 `declaration.Specs()` 直接赋给 `Manifest.Definitions`；生成的 `EchoDefinitionSpec()` / `ClientDefinitionSpec()` 来自同一数据。配置-only 或仅贡献的 definition 不需要虚构服务。

客户端在发送前校验请求，端点在分派前校验收到的数据，这是两个信任边界，都不补默认值。未知字段、未知方法、坏响应及失败的业务调用均明确返回错误。

### 声明流式方法与消费者范围

方法声明里加 `stream`，`response` 描述的就是序列里的一项：

```toml
[[services.methods]]
name = "tail"
stream = true
request = { type = "object", properties = { count = { type = "integer", minimum = 0 } }, required = ["count"] }
response = { type = "object", properties = { line = { type = "string" } }, required = ["line"] }
```

生成的接口方法返回 `iter.Seq2[ExampleFeedTailResponse, error]`，规则同[流式方法](#流式方法)。

`ClientSourceConsumer()` 的范围是声明里的 1.1–1.3，它进入 `ClientDefinitionSpec()` 的 `Requires`，也就是 `ClientPlugin` 的依赖；代码里需要显式范围时用 `ExampleEchoRequirement(1, 3)`。其余规则同[消费者范围](#消费者范围)，`GetExampleEcho(scope)` 取得客户端。

### 可选、null 与联合

普通叶子直接使用 `string/bool/schema.Int64/schema.Uint64/json.Number/[]byte`；跨方法传递普通字符串不必强制转换。`Optional[T]` 表示是否出现，`Nullable[T]` 表示显式 null：`schema.Some(false)` 不等于省略，`schema.NonNull("")` 不等于 null。显式具名约束根保留独立 codec，父对象编码也保留所有叶子约束。

tagged union 使用 `New<Union>Variant<Tag>(value)` 自动填 tag；不能同时设置两个分支。配置编辑器关联、JSON Schema 导出限制及完整向量命令见[数据契约参考](contracts.md)。
