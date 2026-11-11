---
title: 生成服务实操
description: 从一份受限 JSON 契约生成 Go 端点与客户端，在内核作用域上发布和调用。
---

# 生成服务实操

一份契约声明同时生成配置类型、插件构造函数、服务端点与客户端，以及 Manifest 需要的 definition 描述。本页从声明到一次真实调用走完全程；字段规则见[数据契约](contracts.md)。

## 建立独立项目

从 Cordis 仓库根执行：

```sh
CORDIS_ROOT="$(pwd)"
mkdir -p /tmp/cordis-generated/echo
cd /tmp/cordis-generated
go mod init example.com/cordis-generated
go mod edit -require=github.com/Cyenoch/cordis@v0.0.0
go mod edit -replace="github.com/Cyenoch/cordis=$CORDIS_ROOT"
```

Windows 使用空目录，并将 replace 指向本地检出的绝对路径。

## 写契约并生成

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

## 注册和调用

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
        return echo.ExampleEchoEndpoint.Provide(s, echo.NewExampleEchoEndpoint(echoServer{prefix: cfg.Prefix}))
    }).Load(root, echo.EchoConfig{Prefix: "> "})
    if err := provider.Wait(ctx); err != nil { return err }

    consumer := echo.ClientPlugin(func(ctx context.Context, s *cordis.Scope, _ echo.ClientConfig) error {
        endpoint, err := echo.ExampleEchoEndpoint.Get(s)
        if err != nil { return err }
        client, err := echo.NewExampleEchoClient(endpoint)
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

## 消费者范围

`ClientSourceConsumer()` 的范围是声明里的 1.1–1.3，它进入 `ClientDefinitionSpec()` 的 `Requires`，也就是 `ClientPlugin` 的依赖：只有 minor 落在范围内的 provider 才会绑定，范围外的 provider 使消费者保持 Waiting。客户端不会把范围缩窄成生成时的 1.1，实际 provider 为 1.2 时仍可调用。代码里需要显式范围时用 `ExampleEchoRequirement(1, 3)`。

`NewExampleEchoClient(endpoint)` 检查端点的服务名和 major，不匹配时返回错误。服务版本、包 SemVer 与会话协议版本相互独立。

端点在内核里就是一个普通服务：进程内的消费者、Host 应用的 `host.Scope()` 和另一个进程里的插件，都用 `ExampleEchoEndpoint.Get(scope)` 取得它，再构造客户端。

## 可选、null 与联合

普通叶子直接使用 `string/bool/schema.Int64/schema.Uint64/json.Number/[]byte`；跨方法传递普通字符串不必强制转换。`Optional[T]` 表示是否出现，`Nullable[T]` 表示显式 null：`schema.Some(false)` 不等于省略，`schema.NonNull("")` 不等于 null。显式具名约束根保留独立 codec，父对象编码也保留所有叶子约束。

tagged union 使用 `New<Union>Variant<Tag>(value)` 自动填 tag；不能同时设置两个分支。配置编辑器关联、JSON Schema 导出限制及完整向量命令见[数据契约参考](contracts.md)。
