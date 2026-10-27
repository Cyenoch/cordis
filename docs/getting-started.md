---
title: 快速开始
description: 构建独立 counter 插件，打包后通过 Cordis CLI 调用，再切换到 WASM。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - go.mod
  - cmd
  - examples/plugins/counter
  - packagehost
  - runtime
---

# 快速开始

先运行仓库里的 counter，再换成自己的插件。本页使用公开 SDK、独立插件 module 和真实进程，不需要宿主专属代码。

## 准备环境

- Go **{{docs.goVersion}}**，版本取自本页代码基线的 `go.mod`。
- Git。克隆后不必初始化 `third_party/cordis` submodule。
- 本页默认在 macOS / Linux 的 shell 操作；Windows 构建命令见各节折叠项。

以下检出本页对应的代码修订，避免后续 main 分支变化影响示例：

```sh
git clone https://github.com/Cyenoch/cordis.git
cd cordis
git checkout {{docs.codeCommit}}
mkdir -p bin
go build -o ./bin/cordis ./cmd/cordis
```

<details>
<summary>Windows PowerShell</summary>

```powershell
git clone https://github.com/Cyenoch/cordis.git
Set-Location cordis
git checkout {{docs.codeCommit}}
New-Item -ItemType Directory -Force bin | Out-Null
go build -o ./bin/cordis.exe ./cmd/cordis
```

后续 `./bin/cordis` 改为 `./bin/cordis.exe`。

</details>

## 构建 counter

插件是独立的 Go 程序，不链接进 CLI：

```sh
mkdir -p bin/counter-package
(cd examples/plugins/counter && go build -o ../../../bin/counter-package/plugin .)
```

<details>
<summary>Windows PowerShell</summary>

```powershell
New-Item -ItemType Directory -Force bin/counter-package | Out-Null
Push-Location examples/plugins/counter
go build -o ../../../bin/counter-package/plugin .
Pop-Location
```

包内文件可以不带 `.exe`；Native Runtime 会创建适合 Windows 的私有执行副本。

</details>

示例实现见 [counter/main.go](https://github.com/Cyenoch/cordis/blob/main/examples/plugins/counter/main.go)。它从配置读取初值，注册 `example.counter@1` 服务，处理 `add` 和 `get` 请求。

## 声明插件包

在 `bin/counter-package/plugin.json` 写入以下内容。`os` / `arch` 必须替换成 `go env GOOS GOARCH` 打印的实际目标，例如 `darwin` / `arm64`、`linux` / `amd64` 或 `windows` / `amd64`。

```json title="bin/counter-package/plugin.json"
{
  "schema": "cordis-go.package/v1",
  "id": "example.counter",
  "version": "1.0.0",
  "publisher": "example.author",
  "protocol": {"major": 1, "minMinor": 0, "maxMinor": 0},
  "definitions": [{
    "id": "counter",
    "requires": [],
    "provides": [{"name": "example.counter", "major": 1, "minor": 0}]
  }],
  "defaultDefinition": "counter",
  "runtime": {
    "kind": "native",
    "artifacts": [{"os": "darwin", "arch": "arm64", "path": "plugin"}]
  },
  "dependencies": [],
  "optionalDependencies": [],
  "capabilities": [],
  "contributions": [],
  "dataVersion": 0
}
```

`cordis-go.*` 是保留的 v1 schema 标识，不要把它改成 `cordis.*`。没有能力声明的本地示例只调用 `add` / `get`；`host` 操作需要宿主注册并授权 `example.greeting`，见[宿主示例](https://github.com/Cyenoch/cordis/tree/main/examples/basic-host)。

## 打包并调用

在仓库根目录运行：

```sh
./bin/cordis pack ./bin/counter-package ./bin/counter.zip
./bin/cordis inspect ./bin/counter.zip
./bin/cordis run -dev-unsigned -definition counter -service example.counter -major 1 -config '{"initial":10}' -input '{"operation":"add","delta":2}' ./bin/counter.zip
```

最后一条命令返回：

```json
{"value":12,"events":0}
```

每次 `run` 都新建临时宿主和实例，不保留上次计数。Windows PowerShell 7.3+ 可使用相同 JSON 单引号写法；旧版 PowerShell 的原生命令参数转义不同，建议升级后再复现。

:::caution[仅限自己的开发产物]
`-dev-unsigned` 只许可这一次调用的精确归档摘要，不建立全局信任。Native 插件具有当前用户权限，不要用它运行不受信任代码。正式交付需要签名和宿主 TrustPolicy。
:::

## 切换到 WASM

同一份 Go 源码可以构建为 WASI command：

```sh
go build -o ./bin/cordis-worker ./cmd/cordis-worker
mkdir -p bin/counter-wasm-package
(cd examples/plugins/counter && GOOS=wasip1 GOARCH=wasm go build -o ../../../bin/counter-wasm-package/plugin.wasm .)
```

复制上节 `plugin.json` 到 `bin/counter-wasm-package/plugin.json`，只把 `runtime` 换成：

```json
"runtime": {"kind": "wasm", "entry": "plugin.wasm"}
```

```sh
./bin/cordis pack ./bin/counter-wasm-package ./bin/counter-wasm.zip
./bin/cordis run -dev-unsigned -worker "$(pwd)/bin/cordis-worker" -service example.counter -major 1 -config '{"initial":10}' -input '{"operation":"add","delta":2}' ./bin/counter-wasm.zip
```

worker 必须使用绝对路径，不会从 PATH 或插件目录查找。返回同样的 `{"value":12,"events":0}`。WASM 不接收宿主目录、环境或任意网络权限；访问宿主能力要走授权代理。

<details>
<summary>Windows PowerShell 构建 WASM</summary>

```powershell
go build -o ./bin/cordis-worker.exe ./cmd/cordis-worker
New-Item -ItemType Directory -Force bin/counter-wasm-package | Out-Null
Push-Location examples/plugins/counter
try {
  $env:GOOS = 'wasip1'
  $env:GOARCH = 'wasm'
  go build -o ../../../bin/counter-wasm-package/plugin.wasm .
} finally {
  Remove-Item Env:GOOS, Env:GOARCH
  Pop-Location
}
```

然后使用 `./bin/cordis.exe` 打包，`run` 的 `-worker` 使用 `(Resolve-Path ./bin/cordis-worker.exe).Path` 获得绝对路径。

</details>

## 继续阅读

- [核心概念](concepts.md)：分清包、定义、实例、激活与作用域。
- [编写插件](plugin-author.md)：服务、依赖、清理与独立交付。
- [接入宿主](host-guide.md)：签名信任、加载、调用和可靠关闭。
- [实操教程](composition-tutorial.md)：子实例、服务依赖、realm、事件和中间件；[访问实操](access-tutorial.md)涵盖权限、任务与存储。
- 遇到错误先查[故障排查](troubleshooting.md)，发布前看[上线维护](operations.md)。
- 完整仓库验收：macOS / Linux 执行 `bash conformance/run.sh`；Windows 执行 `./conformance/run.ps1`。命令包含 race、真实 Native/WASM 和生命周期检查，不只是快速开始。
