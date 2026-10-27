---
title: 接入宿主
description: 在 Go 应用中配置运行时与信任策略，安装、启用、调用插件并可靠关闭宿主。
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - go.mod
  - host.go
  - instance.go
  - cascade.go
  - ownership.go
  - contract
  - packagehost
  - access
  - runtime
  - examples/basic-host
---

# 接入宿主

宿主负责信任策略、能力授权和业务数据；Cordis 管理插件包、作用域、服务路由与激活生命周期。在独立 module 引入 `github.com/Cyenoch/cordis` 即可，不需要导入插件源码。

```sh
mkdir my-host
cd my-host
go mod init example.com/my-host
go get github.com/Cyenoch/cordis@{{docs.packageVersion}}
```

<!-- GitHub 源码阅读：将版本占位符换成本页 front matter 的 codeCommit。 -->

## 最小完整加载流程

以下函数加载一个**已签名的 counter 包**。`trust` 必须含该包的公钥、publisher 与包 ID 授权；`workerPath` 是已构建的 `cordis-worker` **绝对路径**。

```go title="host.go"
package example

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    cordis "github.com/Cyenoch/cordis"
    "github.com/Cyenoch/cordis/contract"
    "github.com/Cyenoch/cordis/packagehost"
    wasm "github.com/Cyenoch/cordis/runtime/wasm"
)

func Run(ctx context.Context, directory, archivePath, workerPath string,
    trust packagehost.TrustPolicy) (result error) {
    host, err := cordis.Open(cordis.Options{
        Directory: directory,
        FrameBytes: contract.MaxFrameBytes,
        Trust: trust,
        Runtimes: map[string]contract.Runtime{"wasm": wasm.New(workerPath)},
    })
    if err != nil {
        return err
    }
    defer func() {
        closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        result = errors.Join(result, host.Close(closeCtx))
    }()

    pkg, err := host.Install(ctx, archivePath)
    if err != nil {
        return err
    }
    if err := host.Select(ctx, pkg.Ref); err != nil {
        return err
    }
    instance, err := host.Enable(ctx, contract.Entry{
        ID: "counter.main", Package: pkg.Ref.ID, Definition: "counter",
        Config: json.RawMessage(`{"initial":10}`),
    })
    if err != nil {
        return err
    }
    if err := instance.Wait(ctx); err != nil {
        return err
    }
    response, err := host.Call(ctx, "", contract.Requirement{
        Name: "example.counter", Major: 1, MinMinor: 0, MaxMinor: 0,
    }, json.RawMessage(`{"method":"execute","input":{"operation":"add","delta":2}}`))
    if err != nil {
        return err
    }
    fmt.Println(string(response))
    return host.Disable(ctx, "counter.main")
}
```

关闭使用独立、有期限的 context，并把失败返回给调用者；不要在业务 context 已取消后直接拿它清理。`Open` 不会偷偷恢复此前启用的插件。

将函数保存为 `host.go`，再执行 `go mod tidy` 和 `go build ./...`。它供应用调用，不是独立 main；签名包、公钥和 worker 的准备方式见[快速开始](getting-started.md)与[包和签名](packages.md)。

## 配置信任策略

```go
trust := packagehost.TrustPolicy{
    Keys: map[string]packagehost.TrustedKey{
        "author-key": {PublicKey: publicKey, Publisher: "example.author"},
    },
    Publishers: map[string][]string{
        "example.author": {"example.counter"},
    },
}
```

`publicKey` 是宿主可信渠道获得的 `ed25519.PublicKey`，不是从待安装包里读取的自报公钥。无签名、错误 publisher、未授权包 ID 与吊销密钥均不应执行。

只在你信任原生代码时把 `native.New()` 加入 `Runtimes["native"]`；不注册即可禁用该形式。Native 拥有当前用户权限，不能靠 Broker 把它变成沙箱。

## 宿主能力默认拒绝

插件必须在 Manifest 声明 capability，宿主再通过 `Options.Authorize` 决定是否授予、授予什么 scope。Broker 同时注册 scope 检查与 handler；不要将示例中的全量授权复制为产品策略。

凭据、文件路径与完整业务对象不应直接返回给插件，handler 只返回批准的数据投影。贡献 validator、任务接管和 namespace 存储同样由宿主接线，见[访问与授权](access.md)。

## 多实例与版本管理

- **同服务多实例**：先配置隔离 realm，再为 Entry 设置各自 `Realm`，避免同一解析空间重复 provider。
- **配置更新**：检查 `instance.Update` 的结果和错误。取消等待不撤销已提交配置；看 `Status.Desired` / `Applied` 判断收敛。
- **包版本切换**：先 `Install` 新版本，再显式 `Select` 或 `Replace`；只安装不改变运行选择。
- **级联操作**：先生成并确认 `PlanCascade`，再显式执行，不把依赖冲突自动转成全量停用。
- **重启恢复**：先恢复 realm、Broker 和 contribution validator，再调用 `host.Restore(ctx)`。

数据迁移与补偿由宿主负责，恢复旧代码不代表可以降低数据版本。操作顺序见[上线维护](operations.md)，精确边界见[包管理](packages.md)、[组合](composition.md)与[恢复指南](recovery.md)。

## 跑通真实宿主

[`examples/basic-host`](https://github.com/Cyenoch/cordis/tree/main/examples/basic-host) 会临时生成开发签名密钥，验证 Native / WASM 的双实例、宿主回调、事件、配置重启和卸载。它用于演示，不是生产密钥服务。

在仓库根执行 `bash conformance/run.sh` 可运行完整示例及生命周期链；Windows 用 `./conformance/run.ps1`。需要只运行具体链时，参考[生命周期验收](lifecycle-acceptance.md)。

不需要外部进程的机制演示见[组合实操](composition-tutorial.md)与[访问实操](access-tutorial.md)，按用途查方法见[API 导航](api.md)。

## 当前句柄、恢复与显式分派

通过 `host.Instance(id)` 管理恢复或替换后的当前实例，`host.Instances()` 取得独立清单；生成客户端可直接接收 `host.Realm("workspace")`。`Restore` 返回 `(contract.RestoreReport, error)`，逐条区分 registered、existing、disabled、blocked、failed、conflict，不因部分失败强制重建已经运行的条目。

`HandleGuest` 仍是完整替换。只接管部分方法时，在 switch 的 default 分支显式调用 `host.DispatchGuest(ctx, scope, method, input)`；业务错误直接返回，不按 error 猜“未处理”，也不自动执行两遍。默认 dispatcher 包含能力、任务、事件及子实例。

`Options.Sink` 接收宿主本地原始 cause、activation、call 和 stage，安全投影仍用于回传 guest。`DiagnosticStderr` 只有显式启用且有 Sink 才转交 Runtime 的有界 stderr chunk；默认只排空。原文只到显式 Redactor，默认不回显。Sink 失败与原错误合并返回；异步失败通过 `host.DiagnosticErrors()` 保留最早最多 64 项，不递归调用失败 Sink。
