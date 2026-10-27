# Cordis

可独立使用的 Go 插件组合与外部包宿主。Go module：`github.com/Cyenoch/cordis`；MIT。参考 cordiverse/cordis 的组合语义，不复制 JavaScript 实现或 Vortex GPL 代码。

## 实际运行

Go 版本要求见 `go.mod`。首次检出可用 `git clone https://github.com/Cyenoch/cordis.git`。以下命令从本目录执行：

```sh
bash conformance/run.sh
```

命令从独立作者 module 构建 native/WASI 产物，运行 race/集成测试、签名包安装和双实例生命周期。宿主构建时不导入插件代码，不需要 VortexGo 或相邻仓库。Windows 使用 `conformance/run.ps1`。

本地开发调用可用 `go run ./cmd/cordis run -dev-unsigned -definition main -service echo -major 1 -min-minor 1 plugin.zip`；未指定 major 时要求声明唯一匹配版本，不假定 1.0。

工具入口为 `go run ./cmd/cordis`，WASI worker 用 `go build -o bin/cordis-worker ./cmd/cordis-worker` 构建。现有 `cordis-go.*` v1 schema 标识与签名域保留原值，不随项目名称改变；参见[包格式](docs/packages.md)与[协议](docs/protocol.md)。

## 宿主入口

```go
host, err := cordis.Open(cordis.Options{
    Directory: storeDirectory,
    Trust: trustPolicy,
    Runtimes: map[string]contract.Runtime{
        "wasm": wasm.New(workerPath),
        "native": native.New(), // 显式允许受信任原生代码
    },
})
if err != nil { return err }
pkg, err := host.Install(ctx, archivePath)
if err != nil { return err }
if err := host.Select(ctx, pkg.Ref); err != nil { return err }
instance, err := host.Enable(ctx, contract.Entry{
    ID: "my.instance", Package: pkg.Ref.ID, Config: json.RawMessage(`{}`),
})
if err != nil { return err }
if err := instance.Wait(ctx); err != nil { return err }
// 配置 broker、realm 和 contribution validator 后，可显式 Restore(ctx)。
// 退出时必须调用 host.Close(ctx) 并处理错误。
```

- `Host` 组合持久包存储、作用域、授权、任务、贡献和 Runtime；核心领域不依赖 UI 或游戏。
- `contract.Scope` 属于单次 activation；服务依赖由宿主绑定，资源使用 `Defer` 或 `contract.NewGroup(scope)`。
- `sdk.Serve` / `ServeDefinitions` 运行外部作者代码；`sdk.Provide` / `sdk.Call` 提供类型化数据边界。多个定义可共享同一包产物，但每次 activation 仍独占进程。
- `Instance.Update` 持久化期望配置并等待收敛；取消等待不撤销已接受更新。失败使用显式 `Retry`；`Host.Supervise` 是显式有限重启，不重放业务调用。
- 作者通过 `contract.CreateChild` 创建同包可选子功能；builtin 用 `Host.RegisterDefinition` 注册定义。子实例等待不阻塞父启动，退役自动清理，不需要导入 internal。
- `Store.Discover` 批量发现并返回冲突诊断；`Store.Runs` 查看中断记录；`Host.PlanCascade/ReplaceCascade/UninstallCascade` 明确规划并确认级联。
- 原生进程不是权限沙箱；WASM 不接收宿主路径、环境或网络权限。资源清理不是业务回滚。

## 文档

[在线文档站](https://cyenoch.github.io/cordis/) · [快速开始](docs/getting-started.md) · [插件作者指南](docs/plugin-author.md) · [宿主接入](docs/host-guide.md) · [CLI](docs/cli.md)

文档面向插件作者和 Host 开发者，包含完整程序、参考及运维指南；页尾标明所述 API 对应的代码修订。

| 主题 | 入口 |
|---|---|
| 动手组合与访问策略 | [组合实操](docs/composition-tutorial.md)、[访问实操](docs/access-tutorial.md) |
| 契约生成实操 | [生成服务](docs/generated-service.md) |
| API、排障与上线维护 | [API 导航](docs/api.md)、[排障](docs/troubleshooting.md)、[上线维护](docs/operations.md) |
| 作者与受限类型生成 | [contracts](docs/contracts.md)、[counter](examples/plugins/counter/) |
| 作用域、依赖和更新 | [composition](docs/composition.md)、[resources](docs/resources.md) |
| 外部包、签名、恢复 | [packages](docs/packages.md)、[recovery](docs/recovery.md) |
| 双向协议与 Runtime | [protocol](docs/protocol.md)、[runtimes](docs/runtimes.md) |
| 授权、任务、存储和脱敏 | [access](docs/access.md) |
| 性能复现 | [performance](docs/performance.md) |
| 真实外部生命周期与回归 | [lifecycle acceptance](docs/lifecycle-acceptance.md)、[regressions](docs/regressions.md) |

规划与行为规范唯一来源：[OpenSpec change](openspec/changes/build-go-plugin-framework/)。验证结果以 change 的任务证据为准；迁出前的平台记录不代表本次重命名后已重新验证。宿主专属 ABI、Adapter 和游戏验收由宿主项目维护。

参与仓库或文档开发见[贡献指南](CONTRIBUTING.md)。
