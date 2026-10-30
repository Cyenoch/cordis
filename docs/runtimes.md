---
title: Native 运行时
description: 独立原生进程、取消与进程回收。
---

# Native 运行时

每次外部 activation 使用一个目标平台的原生进程，通过 stdin/stdout 交换标准会话帧。插件包的 `runtime.artifacts` 明确列出 OS、架构和相对路径，不选择其他平台或静默降级。

## 启动与退出

Host 从已验证包创建不可变执行快照。Runtime 的 Start context 只限制启动，不成为 activation 生命周期期限；需要进程上级生存期时使用 `native.Options.Lifetime`。

初始化成功后，Host 校验提供的服务并发送发布确认，SDK 才启动受管工作。退役先通知业务取消，再等待消费者清理；最终 shutdown 执行插件清理。超出宽限后终止并回收进程树。

## 环境与输出

默认不继承父进程环境；显式环境由 `native.Options.Environment` 指定。stdout 专用于协议，stderr 始终被排空，避免写满管道阻塞插件。配置 `Stderr` 回调可接收输出，转发受字节预算限制，超出的部分仍持续排空。

运行时注入 `CORDIS_PACKAGE_DIR`（Go 常量 `native.PackageDirectoryEnv`），值为当前 activation 已验证包快照的绝对路径。插件可用 `filepath.Join(os.Getenv("CORDIS_PACKAGE_DIR"), "assets", "message.txt")` 读取声明资源；目录保留到该激活清理完成。显式 Environment 不能覆盖此保留变量。工作目录仍是独立临时目录，包快照也不是持久业务目录或权限沙箱。

## 权限边界

native 插件拥有启动用户权限。包签名证明来源与内容完整性，进程回收处理资源生命周期，两者都不提供操作系统权限沙箱。

[协议](protocol.md) · [生命周期验收](lifecycle-acceptance.md)
