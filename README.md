# Cordis

Go 的插件组合框架，参考 cordiverse/cordis 的组合语义。module 为 `github.com/Cyenoch/cordis`，MIT 许可。

仓库由一个组合内核和建在它上面的 Host 组成：

- **进程内内核**（根包 `cordis`）：只依赖标准库。插件在一个进程里组成实例树，用类型化的键交换普通的 Go 值，支持服务依赖与隔离、事件、查询、中间件链、清理和配置更新。见[进程内内核](docs/kernel.md)，以及建在它上面的[配置文件加载器](docs/loader.md)、[日志服务](docs/logging.md)和[定时辅助](docs/timers.md)。
- **Host 与 native 插件**（`hosting` 包）：管理签名包和持久实例。同一个 `cordis.Plugin` 既可以在应用进程里加载，也可以作为 native 包独占一个进程，经 JSON 协议交换数据；两者在 Host 的内核里共享同一个服务空间。跨进程的服务、事件、查询、中间件链和流式结果用 `contract` 里的键。

项目处于 alpha 阶段，API 和格式直接随设计收敛。

## 使用

预编译 CLI 见 [v0.1.0-alpha.2](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.2)，提供 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 归档及校验和。

Go 版本见 `go.mod`。也可从本地检出构建 CLI：

```sh
go build -o ./bin/cordis ./cmd/cordis
```

作者通过 `cordis init` 创建独立 module，用一份契约生成配置类型、`<Def>Plugin(start)` 和类型化服务，再执行 `build`、业务测试、`pack`。完整可运行步骤见[快速开始](docs/getting-started.md)。

`cordis dev` 监视单包源码，也可用 project 清单联合开发多个包；整组构建和预检通过后再重启。使用者可通过 CLI 签名、离线 install/select/enable，再用 `cordis host` 前台恢复运行；显式开启本机控制后，可在线查看状态和启停实例。参数见 [CLI](docs/cli.md)。

宿主加载一个已签名的 native 包：

```go
host, err := hosting.Open(hosting.Options{
    Directory: storeDirectory,
    Trust: trustPolicy,
})
if err != nil { return err }
pkg, err := host.Install(ctx, archivePath)
if err != nil { return err }
if err := host.Select(ctx, pkg.Ref); err != nil { return err }
instance, err := host.Enable(ctx, contract.Entry{
    ID: "counter", Package: pkg.Ref.ID, Config: json.RawMessage(`{}`),
})
if err != nil { return err }
if err := instance.Wait(ctx); err != nil { return err }
```

退出时调用 `host.Close(ctx)` 并处理错误。完整宿主程序见 [examples/basic-host](examples/basic-host/main.go)。

## 组合模型

- 依赖就绪后启动实例，provider 退役时先清理消费者；新 provider 发布后重新绑定。
- 服务默认共享；`Scope.Isolate`、`Entry.Isolate`、`Child.Isolate` 为指定服务建立子树边界，`Host.Caller` 选择某个 entry 的解析视图。
- `sdk.CreateChild` 创建归属于当前父激活的同包子实例；Host 可枚举、查询及控制动态子树。
- 事件、查询和中间件链的登记返回取消句柄，支持 `Once`、`Prepend`；事件在 Host 内广播。流方法在契约里声明，生成 `iter.Seq2`。
- `Scope.Defer` 绑定清理，按逆序执行；`Scope.Go` 管理随激活取消的后台工作。
- `Instance.Update` 校验并更新配置；`Host.Retry` 保持配置重试已清理的静态 Failed 实例。自动恢复默认关闭，可按持久 native 实例配置有限退避重试。
- native 插件入口是 `sdk.Run(ctx, sdk.Define(<Def>Plugin(start)))`。每次激活独占一个进程，标准协议处理调用、取消和清理。

包管理只保存代码、配置意图和运行记录。授权、任务、业务存储、贡献和脱敏由应用实现。native 插件具有启动用户权限，签名证明来源与完整性；资源清理不会回滚业务副作用。

可选 slog 记录生命周期和失败调用，状态及运行记录保留归属与退出摘要；`Host.Done()` 区分“关闭已完成”和“返回了历史错误”。本机控制和有限恢复见对应文档；无缝升级、市场和 HMR 不在当前范围。

## 验证

```sh
bash conformance/run.sh
```

Windows 使用 `conformance/run.ps1`。脚本运行 integration/race 测试，从独立作者 module 构建原生插件，覆盖签名包与资源、离线/在线 CLI、多包开发重启、有限故障恢复、动态子树、服务隔离、事件、取消、清理和重开恢复。宿主构建不导入插件业务代码。

## 文档

[在线文档](https://cyenoch.github.io/cordis/) · [进程内内核](docs/kernel.md) · [编写插件](docs/plugin-author.md) · [接入宿主](docs/host-guide.md) · [API](docs/api.md) · [CLI](docs/cli.md)

[发布插件](docs/plugin-release.md) · [多包开发](docs/multi-package-dev.md) · [本机控制](docs/local-control.md) · [故障恢复](docs/recovery.md)

[组合实操](docs/composition-tutorial.md) · [生成服务](docs/generated-service.md) · [包格式](docs/packages.md) · [协议](docs/protocol.md) · [排障](docs/troubleshooting.md)

文档与当前源码一起验证。[主规格](openspec/specs/) 描述当前契约，[变更记录](openspec/changes/) 保存设计、任务与实际验证证据；归档的变更不代表当前能力或本轮平台结果。参与开发见[贡献指南](CONTRIBUTING.md)。
