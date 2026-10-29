---
title: 生成服务实操
description: 从一份受限 JSON 契约生成 Go client/server，在真实 Host Scope 上注册和调用。
---

# 生成服务实操

同一份作者声明生成配置、typed Start、service client/server 和 Manifest 的 definition 元数据。生成代码只依赖标准库、`contract` 和 `schema`；字段规则见[数据契约参考](contracts.md)。

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

`version:1` 是 1.0；`"1.1"` 是 1.1，不接受浮点数 `1.1`、补丁位或旧 `major/minor`。consumer 范围必须显式声明；引用同文档服务确定 major 与方法契约，不由 provider 版本猜范围。

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

生成前统一检查符号并运行 gofmt；失败不会写半份生成内容。这里的 `os.WriteFile` 是教程的本地写文件步骤，不提供原子替换保证。多个声明可一起传给 `GenerateMany`；独立调用 `Generate` 的文件也可放在同包，不会各自重定义 Caller/Registrar。指纹常量按 definition/service 命名，分次调用 `GenerateMany`（包括 CLI generate）也不会引入固定公共常量；完整声明可通过 `declaration.Fingerprint()` 取得。

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

func run() (result error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    directory, err := os.MkdirTemp("", "cordis-generated-")
    if err != nil { return err }
    defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
    host, err := cordis.Open(cordis.Options{Directory: directory})
    if err != nil { return err }
    defer func() {
        closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
        defer stop()
        result = errors.Join(result, host.Close(closeCtx))
    }()
    provider, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "echo", Config: json.RawMessage(`{"prefix":"> "}`),
    }, echo.NewEchoDefinition(func(_ context.Context, scope contract.Scope, cfg echo.EchoConfig) error {
        return echo.RegisterExampleEchoServer(scope, echoServer{prefix: cfg.Prefix})
    }))
    if err != nil { return err }
    if err := provider.Wait(ctx); err != nil { return err }
    consumer, err := host.AddBuiltin(ctx, contract.Entry{
        ID: "client", Config: json.RawMessage(`{}`),
    }, echo.NewClientDefinition(func(ctx context.Context, scope contract.Scope, _ echo.ClientConfig) error {
        client, err := echo.NewExampleEchoClient(scope, echo.ClientSourceConsumer())
        if err != nil { return err }
        output, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{Text: "hello"})
        if err != nil { return err }
        fmt.Println("echo:", output.Text)
        if _, err := client.Echo(ctx, echo.ExampleEchoEchoRequest{}); err == nil {
            return errors.New("empty text should fail schema validation")
        }
        fmt.Println("empty text rejected")
        return nil
    }))
    if err != nil { return err }
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

client 在发送前校验 request，server 在分派前校验另一端的数据。这是两个信任边界，不补默认值。未知字段、未知方法、缺失 input、坏 response 及失败的业务调用均明确返回错误。

## 消费者范围与契约身份

`ClientSourceConsumer()` 的范围是声明里的 1.1–1.3。client 不缩窄成生成时的 1.1；如果实际 provider 为兼容的 1.2，仍可按该范围路由。简单调用也可显式使用 `ExampleEchoConsumer(1, 3)`；构造器返回错误，不静默修正范围。

`contract.Consumer` 包含 Requirement 与本地方法契约指纹。client 检查服务名、major、范围是否包含声明 minor，以及方法身份；同名同 major 的另一份方法 schema 不会仅凭版本通过。身份不包含 provider minor，不要求 provider 整体指纹相等，也不证明语义兼容。包 SemVer、服务版本和会话协议各自独立。

Host 直接实现 `contract.Caller`，可以交给生成 client；插件使用自身 Scope 的依赖绑定。生成 client 不依赖具体 SDK Client。

## 可选、null 与联合

普通叶子直接使用 `string/bool/schema.Int64/schema.Uint64/json.Number/[]byte`；跨方法传递普通字符串不必强制转换。`Optional[T]` 表示是否出现，`Nullable[T]` 表示显式 null：`schema.Some(false)` 不等于省略，`schema.NonNull("")` 不等于 null。显式具名约束根保留独立 codec，父对象编码也保留所有叶子约束。

tagged union 使用 `New<Union>Variant<Tag>(value)` 自动填 tag；不能同时设置两个分支。配置编辑器关联、JSON Schema 导出限制及完整向量命令见[数据契约参考](contracts.md)。
