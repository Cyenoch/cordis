---
title: 编写插件
description: 单一 SDK 入口、生成契约与激活资源。
---

# 编写插件

先按[快速开始](getting-started.md)创建独立 Go module。作者只维护 `plugin.toml`、契约声明和业务代码；生成器提供配置类型、服务端接口及客户端。

## 进程入口

```go
func main() {
    if err := sdk.Run(context.Background(), NewMainDefinition(start)); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

`sdk.Run` 支持多个定义，Host 在初始化时选择其中一个。每次 activation 独占进程；stdout 只传协议，日志写 stderr。Run 返回清理或运行错误，由 main 决定退出方式。

## 依赖与清理

在契约中声明所需服务，在 Start 中取得类型化客户端并注册 provider。不要缓存跨 activation 的 Scope。初始化期间注册的资源，即使后续初始化失败也会清理。

```go
resource, err := openResource()
if err != nil { return err }
_, err = contract.Own(scope, resource)
return err
```

`Own` 用于接管已有的 `Close() error` 资源，登记失败会立即关闭；需要自定义 context 的清理使用 `scope.Defer`。清理不是业务事务回滚。

## 可选子实例

通过 `contract.CreateChild(ctx, scope, contract.Child{ID: "optional", Definition: "feature"})` 创建同包定义。它先登记，父发布后才启动。不要在父 Start 内等待子启动；子依赖缺失不会阻塞父和兄弟实例。

## 编辑与重启

在单一本机 target 的作者目录完成 `go mod tidy` 后，运行：

```sh
cordis dev -dev-unsigned -go-package . -config-file ../development.toml .
```

保存 Go、契约、资源或配置文件会串行构建并重启；模块外依赖需显式 `-watch ../shared`。一次运行一个 definition，可用 `-definition` 选择；其动态同包 children 由父生命周期管理。

无需每次改发行版本：每个候选使用作者目录外的独立临时 Store 和精确摘要许可，正式 Store 仍拒绝同版本不同摘要。编译、打包或配置校验失败保留旧 activation；新代码通过预检并关闭旧实例后，若初始化失败，只保留可诊断的 Failed，修复文件后再试。关闭历史错误与实际完成分开判断，未完成清理不会启动下一候选。

Build 会更新作者目录中的生成代码、产物与 build.json，不承诺源码事务；Pack 包含源目录的全部合法文件。配置可在目录外单独监视，日志、密钥及运行状态也放在外部。临时根须位于监视目录之外，完成退出后临时候选自动清理。完整参数和边界见 [CLI](cli.md#编辑后自动重启)。

## 测试

用 `plugintest.New` 运行真实 SDK 初始化、生成 codecs 和清理；依赖通过 `plugintest.Options.Business` 提供普通服务测试替身。进程、信号和安装行为由真实 native 集成测试验证。

[配置和服务类型](contracts.md) · [资源清理](resources.md) · [native 运行时](runtimes.md)
