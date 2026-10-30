---
title: 多插件联合开发
description: 把多个本地包放进一个开发 Host，整组构建和重启。
---

# 多插件联合开发

`cordis dev -project FILE` 让多个本地插件共享一个开发 Host。代码或配置变化后先准备完整候选，成功后才关闭旧组；每次切换都重启整组。

## 开发清单

下面的 TOML 对应两个独立作者目录，路径相对于清单文件。也支持 JSON 和 YAML，按扩展名解析，不合并环境变量。

```toml
[[packages]]
directory = "./provider"
goPackage = "."

[[packages]]
directory = "./consumer"
goPackage = "."

[[entries]]
id = "provider"
package = "example.provider"

[[entries]]
id = "consumer"
package = "example.consumer"
configFile = "./consumer.toml"
```

每项 package 必须明确指定 Go 构建包；entry 的 package 是作者声明 ID。entry 可指定 definition、parent、isolate；config 和 configFile 二选一，省略时用 `{}`。所有列出的 entry 都启用。必需包依赖必须在这份清单里，排列顺序不影响选择；服务缺少 provider 时实例保持 Waiting。

```sh
cordis dev -dev-unsigned -project ./development.toml
```

project 模式不能再传单包目录、go-package、definition 或 config flags。`-watch` 仍可额外指定模块外输入，命令行 watch 相对当前工作目录；清单的 `watch = ["../shared"]` 相对清单目录。

## 运行仓库示例

`examples/plugins/provider` 提供 `example.message@1`；独立的 `consumer` 包依赖它，每 200ms 调用一次服务并将结果写入配置的文件。清单是 `examples/development.toml`。

1. 查看 `go env GOOS GOARCH`，将两个作者文件的唯一 native target 改成对应 OS/arch。dev 不会自动删改多目标声明。
2. 把清单中的 output 改成实际绝对文件路径，放在监视目录之外。该路径属于应用配置，不会自动相对于 project 解析。
3. 从仓库根目录启动：

```sh
go run ./cmd/cordis dev -dev-unsigned -project ./examples/development.toml
```

输出文件会出现 `hello`。将 provider 的返回值改为 `updated` 并保存，整组重启后文件会更新。故意写一个编译错误时，旧组继续调用和写文件；修正后再切换。退出 dev 会停止两个进程，但示例写出的业务文件由使用者保留或删除。

## 切换与失败

每次候选都使用新的临时 ZIP 和 Store，只许可此次本地产物的确定摘要。编译、打包、包依赖、配置和父子图校验全部完成后，才关闭旧 Host。输入在构建或关闭期间又变化时，过期候选不会启动。

新组启动后发生初始化错误，会显示实际 Failed/Waiting 状态并等待下一次编辑；旧进程和内存不会恢复。清理尚未完成时停止切换并保留目录，不能因等待超时就复用仍被占用的资源。

源码、资源、清单和配置都参与监视；全部包的生成代码与产物共同排除，避免互相触发重建。清单写坏时仍监视它，修正后继续。未列出的远程包不会下载，Go replace 指向的模块外目录需显式 watch。

监视通过轮询内容和修改时间识别保存，包括改动后撤回原文。它不冻结整个 Go 文件系统；外部工具若在两次扫描间改写又恢复内容和时间戳，轮询无法观察到这次变化。

单包开发命令继续可用。project 清单只服务本地开发，不是生产部署文件，也不提供 HMR、局部切换或配置回写。
