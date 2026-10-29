---
title: 快速开始
description: 从 TOML 声明生成独立插件，完成构建、调试和签名包前台运行。
---

# 快速开始

从一份 TOML 作者声明生成配置、Start 包装和服务描述，业务只写 Go。以下使用包含本轮功能的本地 Cordis 检出；目前尚无发布版本，不使用 `@latest` 或虚构的 SDK 版本。

## 准备工具

需要 Go **{{docs.goVersion}}** 和 Git；不必初始化 `third_party/cordis` submodule。在 Cordis 仓库根目录执行：

```sh
CORDIS_ROOT="$(pwd)"
mkdir -p bin
go build -o ./bin/cordis ./cmd/cordis
CORDIS="$CORDIS_ROOT/bin/cordis"
```

本页 shell 命令面向 macOS / Linux。Windows 使用 PowerShell、`cordis.exe`，本地检出的路径也必须是绝对路径。

## 初始化独立 module

选择一个尚不存在的目录：

```sh
PLUGIN="$(mktemp -d)/hello"
"$CORDIS" init -module example.net/cordis-hello -sdk-version v0.0.0 -id example.hello -publisher example.author "$PLUGIN"
cd "$PLUGIN"
go mod edit -replace="github.com/Cyenoch/cordis=$CORDIS_ROOT"
```

`v0.0.0` 只是本地 module 占位，实际依赖由 `replace` 指向刚才的真实检出；不是可下载安装的发行版。`init` 写入 `plugin.toml`、独立 `go.mod`、业务入口和启动测试，不覆盖已有文件，也不授予原生执行信任。

## 声明契约

保留 `plugin.toml` 中生成的发行身份和原生运行目标。在第一个 `[runtime]` 前加入下面一行，并删除文件末尾原有的 `[[contract.definitions]]` 及其 `id` 行：

```toml title="plugin.toml（添加到顶层）"
contracts = ["contract.toml"]
```

包发行版本只在 `plugin.toml` 维护；服务版本只在下面的契约维护，不复制进业务 Go 或机器 Manifest。原生目标使用 `init` 写入的当前 `GOOS/GOARCH`，交叉编译时需同时修改声明和构建参数。

```toml title="contract.toml"
[[definitions]]
id = "main"
provides = ["example.echo"]
config = { type = "object", properties = { prefix = { type = "string" } } }

[[services]]
name = "example.echo"
version = "1.1"

[[services.methods]]
name = "echo"
request = { type = "object", properties = { text = { type = "string", minLength = 1 } }, required = ["text"] }
response = { type = "object", properties = { text = { type = "string" } }, required = ["text"] }
```

替换 `main.go`，保留 `init` 生成的 `main_test.go`：

```go title="main.go"
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/Cyenoch/cordis/contract"
    "github.com/Cyenoch/cordis/sdk"
)

type echo struct { prefix string }

func (e echo) Echo(_ context.Context, input ExampleEchoEchoRequest) (ExampleEchoEchoResponse, error) {
    return ExampleEchoEchoResponse{Text: e.prefix + input.Text}, nil
}

func start(_ context.Context, scope contract.Scope, config MainConfig) error {
    prefix := "hello, "
    if config.Prefix.Present {
        prefix = config.Prefix.Value
    }
    return RegisterExampleEchoServer(scope, echo{prefix: prefix})
}

func main() {
    if err := sdk.Run(context.Background(), NewMainDefinition(start)); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

配置校验和解码由生成的 `MainConfig` / `NewMainDefinition` 完成；`sdk.Run` 处理协议、信号和清理，错误交给 main，stdout 留给协议。

## 构建、测试、打包

仍在独立插件目录运行：

```sh
go mod tidy
"$CORDIS" build -go-package . -goos "$(go env GOOS)" -goarch "$(go env GOARCH)" .
go test .
"$CORDIS" pack . "$PLUGIN.zip"
"$CORDIS" inspect "$PLUGIN.zip"
```

`build` 先生成 `cordis_gen.go`，再编译明确目标，并记录实际 SDK、声明和产物摘要到 `build.json`。`pack` 验证这些记录，在私有 staging 生成机器 `plugin.json`；不要手写或复制它回作者目录。修改契约或业务代码后重新执行 `build`、`test`、`pack`。

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
{"text":"hello, Cordis"}
```

每次 `run` 都新建临时宿主和实例。启动等待默认有期限，等待诊断写入 stderr，调用结果写入 stdout；需要时用 `-startup-timeout 1m` 调整启动期限。

:::caution[仅限自己的开发产物]
`-dev-unsigned` 只许可本次归档的精确摘要，不建立全局信任。Native 插件具有当前用户权限，不是沙箱；不要运行不受信任代码。正式交付需要签名和宿主 TrustPolicy。
:::

TOML 没有 null，也不能表达非对象根。契约需要显式 null、数组或标量根时改用 YAML 文件，再由 `-config-file` / `-input-file` 读取；省略字段、空字符串和字符串 `"null"` 都不是 null。JSON 适合工具交换，内联参数始终是 JSON：

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

按 [CLI 信任文件示例](cli.md#离线管理与前台运行)填写 `trust.json`，把 `publicKey` 替换为 keygen 的输出，publisher 使用 `example.author`，包 ID 使用 `example.hello`。下面 select 的完整引用取自 sign 输出：

```sh
"$CORDIS" install -store "$PLUGIN.state" -trust "$PLUGIN.trust.json" "$PLUGIN.zip"
"$CORDIS" select -store "$PLUGIN.state" -trust "$PLUGIN.trust.json" 'example.hello@0.1.0#完整摘要'
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
- [数据契约](contracts.md)、[生成服务](generated-service.md)：配置类型、服务范围和编辑器 schema。

仓库示例 counter 与 chain 各自使用独立 module。`go generate ./examples/pluginmeta` 同时更新示例契约与 Host 元数据；`bash conformance/run.sh` 验证真实 native 进程，Windows 使用 `conformance/run.ps1`。
