---
title: 快速开始
description: 从 TOML 声明生成独立插件，完成 init、build、test、pack、run，再切换到 WASM。
codeCommit: 727f8f4e2c8df9429ec47b3ba0e04ec287ca276a
codePaths:
  - go.mod
  - cmd
  - config
  - schema
  - sdk
  - plugintest
  - examples/pluginmeta
  - examples/plugins/counter
  - packagehost
  - runtime
---

# 快速开始

从一份 TOML 作者声明生成配置、Start 包装和服务描述，业务只写 Go。以下使用包含本轮功能的本地 Cordis 检出；目前尚无发布版本，不使用 `@latest` 或虚构的 SDK 版本。

## 准备工具

需要 Go **{{docs.goVersion}}** 和 Git；不必初始化 `third_party/cordis` submodule。在 Cordis 仓库根目录执行：

```sh
CORDIS_ROOT="$(pwd)"
mkdir -p bin
go build -o ./bin/cordis ./cmd/cordis
go build -o ./bin/cordis-worker ./cmd/cordis-worker
CORDIS="$CORDIS_ROOT/bin/cordis"
```

本页 shell 命令面向 macOS / Linux。Windows 使用 PowerShell、`cordis.exe` 和 `cordis-worker.exe`，本地检出的路径也必须是绝对路径。

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
    sdk.Main(NewMainDefinition(start))
}
```

配置校验和解码由生成的 `MainConfig` / `NewMainDefinition` 完成；`sdk.Main` 处理协议、信号和错误退出，stdout 留给协议。

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

## 切换到 WASM

将作者 module 复制到另一个目录，继续复用同一份契约和业务：

```sh
cp -R "$PLUGIN" "$PLUGIN-wasm"
cd "$PLUGIN-wasm"
```

在副本的 `plugin.toml` 中，只把 `[runtime]` 和 `[[runtime.artifacts]]` 两段替换成：

```toml title="plugin.toml（WASM runtime）"
[runtime]
kind = "wasm"
entry = "plugin.wasm"
```

保留其余身份、契约引用和独立 `go.mod` 的 replace。然后生成并构建 WASI command：

```sh
"$CORDIS" build -go-package . -goos wasip1 -goarch wasm .
go test .
"$CORDIS" pack . "$PLUGIN-wasm.zip"
"$CORDIS" run -dev-unsigned -worker "$CORDIS_ROOT/bin/cordis-worker" -service example.echo -config-file config.toml -input-file input.toml "$PLUGIN-wasm.zip"
```

结果仍为 `{"text":"hello, Cordis"}`。worker 使用绝对路径，不从 PATH 或插件目录查找。WASM 默认不获得宿主文件、环境或任意网络权限；宿主能力走授权代理。Windows 的构建参数同样是 `-goos wasip1 -goarch wasm`，无需修改全局环境变量；worker 指向绝对路径的 `cordis-worker.exe`。

## 继续阅读

- [数据契约](contracts.md)、[生成服务实操](generated-service.md)：配置约束、消费者范围和编辑器 schema；TOML 编辑器使用 Taplo `#:schema`，YAML 使用 language-server modeline，JSON 用文件关联，不往闭合业务对象塞 `$schema`。
- [编写插件](plugin-author.md)、[接入宿主](host-guide.md)：能力、清理、签名信任和可靠关闭。
- [组合实操](composition-tutorial.md)、[访问实操](access-tutorial.md)：子实例、realm、事件、任务与存储。
- 仓库示例 `examples/plugins/counter` / `chain` 各自是独立 module；`go generate ./examples/pluginmeta` 从它们的作者源同时更新插件代码与宿主元数据，真实验收保留 Native/WASM 的原始调用和故障场景。
- 完整仓库验收：macOS / Linux 执行 `bash conformance/run.sh`，Windows 执行 `./conformance/run.ps1`；不以业务 harness 测试代替真实运行时验收。
