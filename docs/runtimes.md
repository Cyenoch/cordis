---
title: Native 运行时
description: 每次激活一个独立的原生进程：启动、输出、退出与权限边界。
---

# Native 运行时

native 运行时让外部插件作为独立的操作系统进程运行，并保证它的启动、取消和回收是确定的。每次激活使用一个目标平台的进程，通过 stdin/stdout 交换[会话协议](protocol.md)的帧。包的 `runtime.artifacts` 明确列出 OS、架构和相对路径，不选择其他平台，也不降级。

## 启动与退出

Host 从已验证的包创建不可变的执行快照，在其中启动产物，并发送 `initialize`。插件进程里的内核启动定义、确认声明的服务都已发布后，Host 才把它们接入自己的服务空间。`Runtime.Start` 的 context 只限制启动，不是激活的生命周期期限；需要进程的上级生存期时用 `native.Options.Lifetime`。

退役先让依赖它的消费者完成清理，再向插件发送 `shutdown`：SDK 取消普通工作、等待已准入的回调、按逆序运行清理。超出宽限（`hosting.Options.Grace`，默认 5 秒）仍未退出时终止并回收整个进程树。进程在没有收到停止请求时退出（包括退出码 0），该 entry 以 `unexpected_exit` 失败。

进程树是 Unix 的进程组或 Windows 的 Job。后代自行 `setsid`、另建进程组或 daemonize 后就不在树内，宿主不回收它们。

## 环境与输出

默认不继承父进程环境，需要的变量由 `native.Options.Environment` 显式给出。stdout 专用于协议；stderr 始终被排空，避免写满管道阻塞插件。配置 `hosting.Options.Stderr` 可接收输出，转发受 `StderrBytes` 预算限制，超出的部分继续排空。

运行时注入 `CORDIS_PACKAGE_DIR`（Go 常量 `native.PackageDirectoryEnv`），值为当前激活已验证包快照的绝对路径，插件用它读取声明的资源，例如 `filepath.Join(os.Getenv("CORDIS_PACKAGE_DIR"), "assets", "message.txt")`；目录保留到该激活清理完成。显式 `Environment` 不能覆盖这个变量。工作目录是独立的临时目录，包快照既不是持久业务目录，也不是权限沙箱。

## 权限边界

native 插件拥有启动用户的权限。包签名证明来源与内容完整性，进程回收管理资源生命周期，两者都不是操作系统权限沙箱。

[协议](protocol.md) · [生命周期验收](lifecycle-acceptance.md)
