---
title: 发布插件
description: 精确选择发行文件，构建多平台包并交给宿主安装。
---

# 发布插件

仓库的 `examples/plugins/resource` 是一个完整示例：五种 native 目标共享一份 `assets/message.txt`，通过 `example.resource@1` 的 read 方法返回内容。以下命令从仓库根目录执行，输出和私钥放到示例目录之外。

## 准备工具

可从 [v0.1.0-alpha.1](https://github.com/Cyenoch/cordis/releases/tag/v0.1.0-alpha.1) 下载对应平台 CLI 和 SHA256SUMS。预发布供试用，不表示稳定版兼容承诺。解压前用 SHA-256 工具核对归档，解压后执行 `cordis version` 查看 tag 和来源提交。

本地检出也可以构建相同命令行工具：

```sh
go build -o ./bin/cordis ./cmd/cordis
```

Windows 的输出文件名使用 `./bin/cordis.exe`。后文以 POSIX 命令为例；CLI 参数在各平台相同。

## 声明与构建

作者文件顶层列出额外资源：

```toml
resources = ["assets/message.txt"]
```

每个 native 目标有自己的 artifact 路径。对示例的全部声明目标构建：

```sh
./bin/cordis build -go-package . -goos linux -goarch amd64 ./examples/plugins/resource
./bin/cordis build -go-package . -goos linux -goarch arm64 ./examples/plugins/resource
./bin/cordis build -go-package . -goos darwin -goarch amd64 ./examples/plugins/resource
./bin/cordis build -go-package . -goos darwin -goarch arm64 ./examples/plugins/resource
./bin/cordis build -go-package . -goos windows -goarch amd64 ./examples/plugins/resource
./bin/cordis pack ./examples/plugins/resource ./bin/resource.zip
./bin/cordis inspect ./bin/resource.zip
```

包只含五个产物、声明资源和元数据。生成代码与 build.json 不发布，但仍是 Pack 的必需校验输入。少构建一个目标、改了声明却没重建、生成代码或产物被修改，Pack 都会失败，并保留已有输出。

inspect 的 `files` 是实际文件清单，逐项列出大小和摘要。未声明的源码、开发配置和日志不会自动进入包；资源清单也不支持目录或通配符。

## 签名和授权

以下私钥路径仅为示例，应替换成仓库之外、仅自己可访问的路径。`keygen` 拒绝覆盖已有文件。

```sh
./bin/cordis keygen /private/path/author.private
./bin/cordis sign ./bin/resource.zip author-key /private/path/author.private
./bin/cordis inspect ./bin/resource.zip
```

将 ZIP 和公钥交给接收者。接收者在自己的 `trust.json` 中授权发布者及包 ID，不从签名自动建立信任：

```json
{
  "keys": {
    "author-key": {
      "publicKey": "替换为 keygen 输出的 64 位十六进制公钥",
      "publisher": "example.author",
      "revoked": false
    }
  },
  "publishers": {"example.author": ["example.resource"]}
}
```

```sh
./bin/cordis install -store ./state -trust ./trust.json ./bin/resource.zip
./bin/cordis select -store ./state -trust ./trust.json 'example.resource@0.1.0#替换为安装输出的完整摘要'
./bin/cordis enable -store ./state -trust ./trust.json -package example.resource resource
./bin/cordis host -store ./state -trust ./trust.json
```

前台 Host 运行后，应用可通过 Go Host 的 `Call` 调用 `example.resource@1`，请求为 `{"method":"read","input":{}}`，得到资源文本。CLI 在线管理不提供任意业务 RPC。

示例读取 `CORDIS_PACKAGE_DIR` 指向的本次激活快照，不依赖源码目录或工作目录。自动验收实际构建所有目标、签名安装，再删除作者目录后在本机调用服务。交叉编译成功只证明目标能够构建，各平台运行结论以该提交的 conformance 为准。

## 发布 Cordis CLI

维护者在干净检出中推送明确版本 tag 后，Release workflow 对同一提交运行文档检查和五平台 conformance，构建并执行各平台归档内的 CLI，再上传完整发行集。普通 main 推送不会创建 Release。

```sh
go run ./scripts/release -version v0.1.0-alpha.1 -output ./dist
```

这条本地命令只生成并检查本机平台归档，输出校验和，不上传或创建 tag。已有同名归档拒绝覆盖；CI 收齐五平台产物才公开预发布。公开后的资产不由重跑替换。
