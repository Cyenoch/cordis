---
title: Go 插件框架
description: 从第一个插件到独立宿主，学习如何使用 Cordis 构建可动态组合的 Go 应用。
template: splash
hero:
  title: 插件独立交付，应用持续生长。
tableOfContents: false
lastUpdated: false
prev: false
next: false
---

# Cordis

## 从一段代码，到一个插件系统

Cordis 是独立的 Go 插件框架。插件以 WASM 或原生进程运行，用签名 ZIP 包独立交付。宿主管理安装、版本和权限；插件声明服务与依赖，资源随每次激活一起清理。

从[快速开始](getting-started.md)运行 counter：独立构建、打包、调用，观察返回值从 10 变成 12。无需先理解协议，也不需要重编译宿主。

## 按你的目标阅读

| 你想做什么 | 从哪里开始 |
| --- | --- |
| 理解包、实例与服务怎么组合 | [核心概念](concepts.md) |
| 用 Go 写插件、处理请求和清理资源 | [插件作者指南](plugin-author.md) |
| 在现有 Go 应用中加载外部插件 | [宿主接入指南](host-guide.md) |
| 打包、签名、调试与生成代码 | [CLI 命令](cli.md) |
| 控制插件权限、任务与持久数据 | [授权、任务与存储](access.md) |
| 排查启动、退出与版本切换 | [组合与实例](composition.md)、[恢复指南](recovery.md) |

## 三条边界

- **原生不是沙箱。** Native 插件拥有当前用户权限；签名验证来源，不证明代码安全。
- **清理不是回滚。** Scope 管理资源释放，数据库、文件与其他业务副作用由宿主负责。
- **机制不绑定产品。** Cordis 不依赖 UI、游戏或特定宿主；领域协议和业务事务留在宿主仓库。

## 代码与证据

[GitHub 源码](https://github.com/Cyenoch/cordis) · [Go API](https://pkg.go.dev/github.com/Cyenoch/cordis) · [示例宿主](https://github.com/Cyenoch/cordis/tree/main/examples/basic-host) · [验收记录](https://github.com/Cyenoch/cordis/blob/main/openspec/changes/build-go-plugin-framework/tasks.md)

MIT 许可。本文档描述当前仓库；公开源码与历史验收不等同于首个稳定版本认证，具体修订与平台范围以验收记录为准。
