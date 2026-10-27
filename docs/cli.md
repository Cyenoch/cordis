---
title: CLI 命令
description: Cordis 命令行工具的打包、签名、检查、代码生成与本地调试用法。
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - cmd/cordis
  - cmd/cordis-worker
  - packagehost
  - schema
---

# CLI 命令

从本文代码基线对应的检出构建工具：

```sh
go build -o ./bin/cordis ./cmd/cordis
go build -o ./bin/cordis-worker ./cmd/cordis-worker
./bin/cordis help
```

也可在仓库根使用 `go run ./cmd/cordis`，Windows 输出文件改为 `.exe`。标志必须放在归档路径之前。

## 命令一览

| 命令 | 作用 |
| --- | --- |
| `pack DIRECTORY ARCHIVE.zip` | 校验目录，生成 ZIP 和完整性表 |
| `keygen PRIVATE_KEY_FILE` | 创建 Ed25519 私钥文件，只向 stdout 输出公钥 |
| `sign ARCHIVE.zip KEY_ID PRIVATE_KEY_FILE` | 为归档签名 |
| `inspect ARCHIVE.zip` | 查看包引用和 Manifest，不授权执行 |
| `validate ARCHIVE.zip` | 检查归档格式与完整性，不评估宿主信任 |
| `generate [-package NAME] CONTRACT.json` | 把受限契约生成 Go 源码并写到 stdout |
| `run -dev-unsigned [FLAGS] ARCHIVE.zip` | 在临时宿主内运行自己的本地开发产物 |

## 打包与签名

```sh
./bin/cordis pack ./my-package ./my-package.zip
./bin/cordis keygen ./author.private
./bin/cordis sign ./my-package.zip author-key ./author.private
./bin/cordis validate ./my-package.zip
./bin/cordis inspect ./my-package.zip
```

把公钥交给宿主维护者；私钥存放在仓库外或受控密钥设施，不提交到版本库。`keygen` 拒绝覆盖现有文件。`inspect` / `validate` 不会因为签名存在就信任作者；执行许可由宿主 TrustPolicy 决定。

## 本地运行

```sh
./bin/cordis run -dev-unsigned -definition counter -service example.counter -major 1 -config '{"initial":10}' -input '{"operation":"add","delta":2}' ./bin/counter.zip
```

| 标志 | 说明 |
| --- | --- |
| `-dev-unsigned` | 必须显式给出，仅许可本次精确摘要，不持久化信任 |
| `-worker PATH` | WASM worker 的绝对路径，例如 `"$(pwd)/bin/cordis-worker"`；Native 不需要 |
| `-definition ID` | 包内定义；默认用 Manifest 的默认定义 |
| `-service NAME` | 启动后调用的服务；省略则打印实例状态 |
| `-major N` | 服务主版本；省略时必须能唯一匹配，不能猜测 |
| `-min-minor N` / `-max-minor N` | 兼容 minor 闭区间 |
| `-config JSON` | 插件配置，默认 `{}` |
| `-input JSON` | 请求数据，默认 `{}` |

临时宿主不替你注册产品 capability、contribution validator 或业务存储。需要这些机制时使用[宿主公开 API](host-guide.md)。已签名但未被信任的包也不会由 `-dev-unsigned` 绕过坏信任；正式签名包由宿主 TrustPolicy 加载。

## 生成 Go 契约

```sh
./bin/cordis generate -package pluginapi ./echo.schema.json > ./api.gen.go
gofmt -w ./api.gen.go
```

输入使用 Cordis 的[受限数据契约](contracts.md)，不接受任意 JSON Schema。输出含客户端、服务端注册与严格编码边界，未知 schema 特性会报错。完整项目见[生成服务实操](generated-service.md)。

## 退出码

- `0`：命令完成。
- `1`：校验、信任、I/O、启动或业务调用失败。
- `2`：参数使用错误，例如未知命令、缺少 `-dev-unsigned` 或服务版本匹配不唯一。

错误写 stderr，结构化结果写 stdout，便于脚本区分。取消不回滚已发生的外部副作用。
