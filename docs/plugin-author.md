---
title: 编写插件
description: 使用公开 Go SDK 声明服务、处理依赖和资源清理，并构建独立插件包。
---

# 编写插件

一个 Cordis 外部插件是运行 `sdk.Serve` 的独立 Go 程序。你写服务和生命周期逻辑，SDK 处理会话、调用编码与取消；不需要导入 `internal` 或手写 NDJSON。

## 创建独立 module

```sh
mkdir my-counter
cd my-counter
go mod init example.com/my-counter
go get github.com/Cyenoch/cordis@v0.0.0-20261027032746-e5e1f31da5f6
```

这里固定的是已公开的首个源码版本，不是稳定标签；本页完整 counter 程序可使用该版本。后文的 `contract.CreateChild` 等新 API 尚不包含在该版本中。

如果需要当前文档描述的新 API，先在另一目录检出 Cordis，再在你的插件 module 中显式指向该检出的**绝对路径**：

```sh
go mod edit -replace=github.com/Cyenoch/cordis=/absolute/path/to/cordis
```

将 `/absolute/path/to/cordis` 替换为实际目录；Windows 可使用 `C:/path/to/cordis`。这仅用于本地开发，打包产物不依赖该目录。新版本发布后，使用 `go mod edit -dropreplace=github.com/Cyenoch/cordis` 并 `go get github.com/Cyenoch/cordis@<包含所需API的固定版本>`，不要复制仓库示例的相对 `replace` 到外部项目。

## 提供一个服务

以下完整程序注册 `example.counter@1`。每次激活创建自己的 `value`：

```go title="main.go"
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/Cyenoch/cordis/contract"
    "github.com/Cyenoch/cordis/sdk"
)

type Request struct {
    Delta int `json:"delta"`
}

type Response struct {
    Value int `json:"value"`
}

func main() {
    err := sdk.Serve(context.Background(), contract.Definition{
        ID: "counter",
        Start: func(ctx context.Context, scope contract.Scope) error {
            value := 0
            return sdk.Provide(scope,
                contract.Service{Name: "example.counter", Major: 1},
                func(ctx context.Context, in Request) (Response, error) {
                    value += in.Delta
                    return Response{Value: value}, nil
                })
        },
    })
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

stdout 专用于协议，日志写 stderr。同一实例的受管回调默认串行；自己启动的裸 goroutine 不会因此自动受管。

## 配置与数据边界

从 `scope.Config()` 读取 JSON，用 `contract.Decode` 解码。宿主接受更新前可以通过 Manifest 的 `configSchema` 校验配置；更新导致新激活，不是修改同一实例闭包里的变量。

typed 服务只传可序列化数据，不传函数、指针或任意 Go 对象。64 位整数使用规定的字符串 codec；可选与 nullable 分别表达。复杂服务建议使用[受限契约和代码生成](contracts.md)，避免手写方法 envelope。

## 声明依赖，再调用

把必需服务写进 `Definition.Requires`，并同步声明到 Manifest 对应 definition。核心会在这些服务就绪后执行 `Start`：

```go
required := contract.Requirement{
    Name: "example.cache", Major: 1, MinMinor: 0, MaxMinor: 2,
}

// 在 Start 或受管 handler 中调用；ctx 必须来自当前调用。
result, err := sdk.Call[LookupRequest, LookupResponse](ctx, scope, required, request)
if err != nil {
    return err
}
```

该片段中的 `LookupRequest`、`LookupResponse` 和 `request` 由你的服务契约定义。同步调用形成环时会返回 `reentrant_call`，不要靠新 goroutine 绕过生命周期与依赖约束。

可选功能使用 [`contract.CreateChild`](composition.md)，让同包子实例单独等待。需要多个可启动定义时使用 `sdk.ServeDefinitions`；每次 activation 仍独占进程。

## 登记资源清理

拿到资源后立刻登记释放操作：

```go
if err := scope.Defer(func(cleanupCtx context.Context) error {
    return resource.Close()
}); err != nil {
    return err
}
```

资源有释放顺序时使用[显式资源组](resources.md)。清理使用框架提供的 cleanup context，不复用已经取消的业务 context。清理只负责释放资源，不回滚外部业务写入。

## 构建与交付

先保存上面的 `main.go`，再整理依赖并构建；不要在空 module 中提前运行 `go mod tidy`。

```sh
go mod tidy

# 当前平台 Native 产物
go build -o plugin .

# 可由 Cordis worker 执行的 WASI command
GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm .
```

为产物提供 `plugin.json`，其中 definition ID、requires/provides 与程序保持一致。可以从[快速开始](getting-started.md)复制 Manifest 结构，再按自己的 Request/Response 调整输入。

使用 `cordis pack` 生成带完整性表的 ZIP；`cordis sign` 添加签名。正式宿主必须配置 key → publisher → plugin ID 的授权，签名不是执行许可。详见[包与签名](packages.md)和 [CLI](cli.md)。

## 从真实示例继续

- [counter](https://github.com/Cyenoch/cordis/tree/main/examples/plugins/counter)：配置、服务、事件、宿主能力调用。
- [chain](https://github.com/Cyenoch/cordis/tree/main/examples/plugins/chain)：跨插件依赖、资源清理与可选子功能。
- [生命周期验收](lifecycle-acceptance.md)：真实 Native/WASM 的退出、取消和版本切换行为。
