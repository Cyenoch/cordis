---
title: 编写插件
description: 使用公开 Go SDK 声明服务、处理依赖和资源清理，并构建独立插件包。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - go.mod
  - contract
  - sdk
  - plugintest
  - schema
  - examples/plugins
---

# 编写插件

Cordis 外部插件是以 `sdk.Main` 运行的独立 Go 程序。SDK 处理会话、调用编码与取消，你只需写服务和生命周期逻辑，不必导入 `internal` 或手写 NDJSON。

宿主必须明确配置完整帧预算（4 KiB–1 MiB），SDK 在 Validate/Start 前应用并原值确认，作者无需自己写握手。预算不只是返回值的字节数：JSON 转义、信封和 LF 都计入。普通结果或错误消息超限时仅当前调用收到固定 `plugin_failed`，会话可继续，但业务副作用不会回滚，也不会自动重试。

分页只限制集合条数，单条记录仍可能超限。优先按业务契约返回字段投影或摘要，再通过独立读取取详情；不要截断 JSON 或假设框架会自动分块。最终编码检查仍是裁决，具体失败边界见[协议参考](protocol.md#帧预算与失败范围)。

## 创建独立 module

```sh
mkdir my-counter
cd my-counter
go mod init example.com/my-counter
go get github.com/Cyenoch/cordis@{{docs.packageVersion}}
```

这里固定的是本手册核对过的公开源码版本，不是稳定标签；它包含 `contract.CreateChild` 等文中使用的 API。升级依赖时，先核对目标版本的行为，再更新项目依赖。

<!-- GitHub 源码阅读：将版本占位符换成本页 front matter 的 codeCommit；Go 可直接用 commit 安装。文档站会在构建时填写精确版本。 -->

如果要联调本地尚未发布的修改，先在另一目录检出 Cordis，再在你的插件 module 中显式指向该检出的**绝对路径**：

```sh
go mod edit -replace=github.com/Cyenoch/cordis=/absolute/path/to/cordis
```

将 `/absolute/path/to/cordis` 替换为实际目录；Windows 可使用 `C:/path/to/cordis`。这仅用于本地开发，打包产物不依赖该目录。恢复使用公开版本时，运行 `go mod edit -dropreplace=github.com/Cyenoch/cordis`，再用 `go get` 固定目标版本；不要复制仓库示例的相对 `replace` 到外部项目。

## 提供一个服务

以下完整程序注册 `example.counter@1`。每次激活创建自己的 `value`：

```go title="main.go"
package main

import (
    "context"

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
    sdk.Main(contract.Definition{
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
}
```

stdout 专用于协议，日志写 stderr。`sdk.Main` 处理原生进程的 interrupt/SIGTERM、会话退出及错误退出码；WASI 使用同一入口，不注册原生信号。需要自己处理错误时用 `sdk.Run(ctx, definitions...) error`；`Serve`、`ServeDefinitions` 与 `ServeTransport` 保留，不接管进程信号。初始化和清理失败不会被当成成功退出。

同一实例的服务和事件回调默认串行；显式受管后台工作与这些回调并发，共享业务状态由作者同步。裸 goroutine 不会自动受管。

## 配置与数据边界

优先从 `plugin.toml` 引用单源契约，生成 `New<Name>Definition`、Config 和服务描述；typed Start 直接接收配置，宿主在提交前使用同一 definition 的 `configSchema` 校验，不要再手写 Decode 和第二份字段列表。仅手工定义时才从 `scope.Config()` 取 JSON 解码，并显式提供对应 validator。更新导致新激活，不是修改同一实例闭包里的变量。

typed 服务只传可序列化数据，不传函数、指针或任意 Go 对象。64 位整数使用规定的字符串 codec；可选与 nullable 分别表达。复杂服务建议使用[受限契约和代码生成](contracts.md)，避免手写方法 envelope。

## 声明依赖，再调用

推荐在契约的 definition 中声明显式 consumer 范围，生成描述同时用于 Definition、Manifest 和 typed client，不手工同步三处版本。下面展示低层手工调用的形状；核心会在必需服务就绪后执行 `Start`：

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

可选功能使用 [`contract.CreateChild`](composition.md)，让同包子实例单独等待；完整顺序见[组合实操](composition-tutorial.md)。多个可启动定义传给 `sdk.Main(definitions...)` 或 `sdk.Run(ctx, definitions...)`，每次 activation 仍独占进程。

宿主能力和贡献使用 `contract.InvokeHost[Request, Response](ctx, scope, capability, input)` 与 `contract.Contribute(scope, slot, id, value)`。它们依赖 `HostInvoker` / `Contributor` 小接口；缺少能力时明确报错，不需要断言 `scope.(*sdk.Client)`。这不替代 Manifest 声明和宿主授权。

## 显式受管工作

在 Start 中调用 `contract.Go(scope, work)` 登记后台工作：

```go
return contract.Go(scope, func(workCtx context.Context) error {
    <-workCtx.Done()
    return workCtx.Err()
})
```

Start 的 `ctx` 在回调返回时结束；`scope.Context()` 属于整个 activation。工作只在宿主完成最终发布后启动，Start 成功但发布被拒绝时从未运行。登记仅限 Start，不把常驻工作塞进服务串行执行位，也不把它变成持久 Task。

退役立即取消普通调用和工作，先确认通知，不提前释放资源。自身 shutdown 先等工作返回，再执行资源清理。工作 error/panic 会使会话失败；本地 Run/Serve 保留原始错误，远端宿主只能取得会话已失败及可用的安全错误，不承诺恢复 guest 本地原因。因退役返回对应的 `ctx.Err()` 不算工作故障。SDK 不强杀不合作 Go 工作，也不会在它尚未退出时声称资源已释放。

## 登记资源清理

可关闭资源优先交给 `contract.Own`，登记失败时它会立即尝试关闭并合并两处错误：

```go
resource, err := contract.Own(scope, resource)
if err != nil {
    return err
}
```

自定义清理使用 `scope.Defer`；资源有释放顺序时使用[显式资源组](resources.md)。使用框架传给清理函数的 context，不捕获已经取消的业务 context；清理只释放资源，不回滚外部写入。

## 业务测试

把同一份真实 `contract.Definition` 传给公开 `plugintest`，不另写测试版 Start 或协议握手：

```go
h, err := plugintest.New(t.Context(), definition, plugintest.Options{
    Config: json.RawMessage(`{"initial":4}`),
})
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() {
    if err := h.Close(context.WithoutCancel(t.Context())); err != nil {
        t.Error(err)
    }
})

result, err := sdk.Call[Request, Response](t.Context(), h,
    contract.Requirement{Name: "example.counter", Major: 1}, Request{Delta: 2})
if err != nil || result.Value != 6 {
    t.Fatalf("counter = %+v, %v", result, err)
}
```

测试文件导入 `context`、`encoding/json`、`testing`、`contract`、`sdk` 和 `github.com/Cyenoch/cordis/plugintest`；`definition` 使用你的生产业务定义，上例假设它读取 `initial`。生成 client 也可直接接收 `h`，经过真实生成 codec，不直接绕过验证调用 handler。

`Options.Business` 接收 `contract.Caller` 测试 port，`Options.Host` 接收 `contract.HostInvoker` 测试 port。未配置的能力调用明确失败；配置校验失败不会进入 Start，Start 失败仍清理已登记资源。`h.Retire(ctx)` 只通知取消，`h.Close(ctx)` 等待工作和清理；取消 Close 的等待不会伪报完成，后续可再次等待。`h.Wait(ctx)` 可检查工作失败及其本地原因。

harness 验证配置、业务服务、能力调用和清理；测试 port 不是生产授权。它不验证 realm/provider 绑定、签名、真实 wire 互操作、原生权限或 Native/WASI 执行，不替换协议回归和[真实生命周期验收](lifecycle-acceptance.md)。

## 构建与交付

先保存上面的 `main.go`，再整理依赖并构建；不要在空 module 中提前运行 `go mod tidy`。

```sh
go mod tidy

# 当前平台 Native 产物
go build -o plugin .

# 可由 Cordis worker 执行的 WASI command
GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm .
```

标准交付走 `cordis init` → `cordis build` → `cordis pack`：以推荐的 `plugin.toml` 和契约生成机器 Manifest、配置类型及构建描述，省去重复的 definition ID、requires/provides 和协议字段。完整命令见[快速开始](getting-started.md)。这里的 `go build` 是低层构建示例；自行流水线须按[包参考](packages.md)提供显式构建描述，不把另一个 SDK 的能力当作实际产物能力。

使用 `cordis pack` 生成带完整性表的 ZIP；`cordis sign` 添加签名。正式宿主必须配置 key → publisher → plugin ID 的授权，签名不是执行许可。详见[包与签名](packages.md)和 [CLI](cli.md)。

## 从真实示例继续

- [counter](https://github.com/Cyenoch/cordis/tree/main/examples/plugins/counter)：配置、服务、事件、宿主能力调用。
- [chain](https://github.com/Cyenoch/cordis/tree/main/examples/plugins/chain)：跨插件依赖、资源清理与可选子功能。
- [生命周期验收](lifecycle-acceptance.md)：真实 Native/WASM 的退出、取消和版本切换行为。
- [生成服务实操](generated-service.md)：从一份契约生成可运行的 client/server。
- [访问实操](access-tutorial.md)：宿主能力、贡献、任务与持久数据如何接线。
