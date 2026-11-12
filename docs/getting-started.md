---
title: 快速开始
description: 用 Go 类型声明独立插件，完成生成、构建、调用、持续开发和签名包前台运行。
---

# 快速开始

本页只写 Go：服务、请求、响应和配置是普通类型，`cordis generate` 从它们推导契约并生成适配代码；随后构建、调用、持续开发并以签名包运行。兼容性承诺见[稳定性](stability.md)。

## 准备工具

需要 Go **{{docs.goVersion}}**。安装 CLI：

```sh
go install github.com/Cyenoch/cordis/cmd/cordis@latest
CORDIS="$(go env GOPATH)/bin/cordis"
```

也可使用[发布页](https://github.com/Cyenoch/cordis/releases)的预编译归档。本页 shell 命令面向 macOS / Linux；Windows 使用 PowerShell 与 `cordis.exe`。

## 初始化独立 module

选择一个尚不存在的目录：

```sh
PLUGIN="$(mktemp -d)/hello"
"$CORDIS" init -module example.net/cordis-hello -id example.hello -publisher example.author "$PLUGIN"
cd "$PLUGIN"
```

`go.mod` 依赖与 CLI 相同的 Cordis 版本。`init` 写入 `plugin.toml`、独立 `go.mod`、业务入口、启动测试和生成物 `cordis_gen.go`、`cordis_gen.json`，不覆盖已有文件，也不授予原生执行信任。`plugin.toml` 只保存发行身份和原生运行目标；原生目标使用当前 `GOOS/GOARCH`，交叉编译时需同时修改声明和构建参数。

## 声明服务

替换 `main.go`，保留 `init` 生成的 `main_test.go`：

```go title="main.go"
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/Cyenoch/cordis"
    "github.com/Cyenoch/cordis/sdk"
)

// Echo 是插件提供的服务。
//
//cordis:service example.echo 1.1
type Echo interface {
    Echo(ctx context.Context, request EchoRequest) (EchoResponse, error)
}

type EchoRequest struct {
    Text string `json:"text" cordis:"minLength=1"`
}

type EchoResponse struct {
    Text string `json:"text"`
}

type Config struct {
    Prefix string `json:"prefix,omitempty"`
}

type echo struct{ prefix string }

func (e echo) Echo(_ context.Context, request EchoRequest) (EchoResponse, error) {
    return EchoResponse{Text: e.prefix + request.Text}, nil
}

//cordis:plugin main
//cordis:provides Echo
func start(_ context.Context, s *cordis.Scope, config Config) error {
    return ProvideEcho(s, echo{prefix: config.Prefix})
}

func main() {
    if err := sdk.Run(context.Background(), sdk.Define(MainPlugin)); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

- `//cordis:service example.echo 1.1` 把接口声明为服务 `example.echo`，版本 1.1；方法在线上叫 `echo`（Go 方法名首字母小写）。服务版本只在这里维护，包发行版本只在 `plugin.toml`。
- 请求、响应和配置就是自己的结构体。`json` 标签决定字段名，没有 `omitempty` 的字段必填；`cordis` 标签写约束，这里要求 `text` 非空。规则见[数据契约](contracts.md#用-go-类型写契约)。
- `//cordis:plugin main` 声明插件 `main`，`//cordis:provides Echo` 声明它提供 `Echo`。`start` 收到的 `*cordis.Scope` 与[进程内内核](kernel.md)里的相同，清理、后台工作和服务都登记在它上面；`ProvideEcho` 在它上发布服务。
- `sdk.Run` 处理协议、信号和清理，错误交给 main，stdout 留给协议。服务方法可能被并发调用，实现需要自行保证并发安全。

`plugin.toml` 不写契约：契约由这些类型推导。

## 生成

```sh
go mod tidy
"$CORDIS" generate
```

`generate` 分析当前目录的包，写出 `cordis_gen.go`（`ProvideEcho`、`GetEcho`、`MainPlugin` 等绑定）和 `cordis_gen.json`（推导出的契约）。两个文件都随源码提交：契约的变化会出现在评审的 diff 里。源码有不合法的声明时，生成失败并指出位置，不写任何文件。

## 构建、测试、打包

仍在独立插件目录运行：

```sh
"$CORDIS" build -go-package . -goos "$(go env GOOS)" -goarch "$(go env GOARCH)" .
go test .
"$CORDIS" pack . "$PLUGIN.zip"
"$CORDIS" inspect "$PLUGIN.zip"
```

`build` 先重新生成，再编译明确目标，并记录实际 SDK、声明和产物摘要到 `build.json`。`pack` 验证这些记录，在私有 staging 生成机器 `plugin.json`；不要手写或复制它回作者目录。修改类型或业务代码后重新执行 `build`、`test`、`pack`。

## 调用

对象型、人手维护的配置和输入默认用 TOML：

```toml title="config.toml"
prefix = "hello, "
```

```toml title="input.toml"
method = "echo"

[input]
text = "Cordis"
```

```sh
"$CORDIS" run -dev-unsigned -service example.echo -config-file config.toml -input-file input.toml "$PLUGIN.zip"
```

```json title="调用结果"
{
  "text": "hello, Cordis"
}
```

每次 `run` 都新建临时宿主和实例。启动等待默认有期限，等待依赖时把原因写入 stderr，调用结果写入 stdout；需要时用 `-startup-timeout 1m` 调整启动期限。

:::caution[仅限自己的开发产物]
`-dev-unsigned` 只许可本次归档的精确摘要，不建立全局信任。Native 插件具有当前用户权限，不是沙箱；不要运行不受信任代码。正式交付由宿主 [TrustPolicy](packages.md#信任) 明确授权。
:::

TOML 没有 null，也不能表达非对象根。请求需要显式 null、数组或标量根时改用 YAML 文件，再由 `-config-file` / `-input-file` 读取；省略字段、空字符串和字符串 `"null"` 都不是 null。JSON 适合工具交换，内联参数始终是 JSON：

```sh
"$CORDIS" run -dev-unsigned -service example.echo -config '{"prefix":"hello, "}' -input '{"method":"echo","input":{"text":"Cordis"}}' "$PLUGIN.zip"
printf '%s' '{"prefix":"hello, "}' | "$CORDIS" run -dev-unsigned -service example.echo -config-file - -config-format json -input-file input.toml "$PLUGIN.zip"
```

stdin 只允许一个输入使用，且必须指定格式；内联与对应文件参数不能同时使用。Windows PowerShell 的原生命令 JSON 转义依版本不同，优先使用文件参数。

## 持续开发与签名运行

仍在插件目录，用同一份配置进入编辑循环：

```sh
"$CORDIS" dev -dev-unsigned -go-package . -config-file config.toml .
```

修改 `prefix` 或业务 Go 并保存，stdout 会报告新的构建 generation 与 activation。编译或配置失败保留旧实例；修复后自动重建。Ctrl+C 等待进程清理；更详细的输入范围与失败边界见 [CLI 的 dev 说明](cli.md#编辑后自动重启)。

正式签名流程也不需要编写 Go 宿主。先退出 dev、重新 build/pack，再把密钥和信任文件放在作者目录外：

```sh
"$CORDIS" keygen "$PLUGIN.author.private"
"$CORDIS" sign "$PLUGIN.zip" author-key "$PLUGIN.author.private"
```

按 [CLI 信任文件示例](cli.md#离线管理与前台运行)填写 `trust.json`，把 `publicKey` 替换为 keygen 的输出，publisher 使用 `example.author`，包 ID 使用 `example.hello`。select 的参数是 pack、sign 或 install 输出的 `exact`：

```sh
"$CORDIS" install -store "$PLUGIN.state" -trust "$PLUGIN.trust.json" "$PLUGIN.zip"
"$CORDIS" select -store "$PLUGIN.state" -trust "$PLUGIN.trust.json" 'install 输出的 exact'
"$CORDIS" enable -store "$PLUGIN.state" -trust "$PLUGIN.trust.json" -package example.hello -config-file config.toml hello
"$CORDIS" host -store "$PLUGIN.state" -trust "$PLUGIN.trust.json"
```

Ctrl+C 退出后再查询：

```sh
"$CORDIS" status -store "$PLUGIN.state" hello
```

此时 enabled 仍表示下次恢复的意图，最近运行记录显示已结束的 activation；`offline: true` 明确说明这不是实时进程查询。运行期间离线命令会因目录占用而失败。

## 继续阅读

- [组合实操](composition-tutorial.md)：父子实例、依赖激活与资源清理。
- [插件作者](plugin-author.md)、[宿主接入](host-guide.md)：运行 native 插件和可靠关闭。
- [数据契约](contracts.md)、[生成服务](generated-service.md)：类型映射、约束标签、生成的绑定和服务范围。

仓库示例 `examples/plugins` 下的每个插件各自是独立 module。`go generate ./examples/pluginmeta` 重新生成示例的契约与绑定，并更新 Host 元数据；`bash conformance/run.sh` 验证真实 native 进程，Windows 使用 `conformance/run.ps1`。
