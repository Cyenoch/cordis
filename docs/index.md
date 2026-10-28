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
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - go.mod
  - host.go
  - contract
  - examples/basic-host
  - examples/plugins
---

# Cordis

## 从一段代码，到一个插件系统

Cordis 是独立的 Go 插件框架。插件以 WASM 或原生进程运行，用签名 ZIP 包交付。宿主管理安装、版本和权限；插件声明服务与依赖，资源随每次激活一起清理。

从[快速开始](getting-started.md)运行 counter：独立构建、打包、调用，观察返回值从 10 变成 12。无需先理解协议，也不需要重编译宿主。

## 按你的目标阅读

| 你想做什么 | 从哪里开始 |
| --- | --- |
| 理解包、实例与服务怎么组合 | [核心概念](concepts.md) |
| 用 Go 写插件、处理请求和清理资源 | [插件作者指南](plugin-author.md) |
| 在现有 Go 应用中加载外部插件 | [宿主接入指南](host-guide.md) |
| 打包、签名、调试与生成代码 | [CLI 命令](cli.md) |
| 动手组合子实例、事件和中间件 | [组合实操](composition-tutorial.md) |
| 配置权限、接管任务、持久化和迁移数据 | [访问实操](access-tutorial.md) |
| 从 JSON 契约生成 Go 服务 | [生成服务实操](generated-service.md) |
| 查公开类型、方法与使用顺序 | [Go API 导航](api.md) |
| 排查启动、退出与版本切换 | [故障排查](troubleshooting.md)、[恢复指南](recovery.md) |
| 发布、轮换密钥、升级和备份 | [上线与维护](operations.md) |

## 三条边界

- **原生不是沙箱。** Native 插件拥有当前用户权限；签名验证来源，不证明代码安全。
- **清理不是回滚。** Scope 管理资源释放，数据库、文件与其他业务副作用由宿主负责。
- **机制不绑定产品。** Cordis 不依赖 UI、游戏或特定宿主；领域协议和业务事务留在宿主仓库。

## 代码与证据

[GitHub 源码](https://github.com/Cyenoch/cordis) · [Go API 导航](api.md) · [示例宿主](https://github.com/Cyenoch/cordis/tree/main/examples/basic-host) · [验收记录](https://github.com/Cyenoch/cordis/blob/main/openspec/changes/archive/2026-10-28-build-go-plugin-framework/tasks.md)

MIT 许可。每页代码基线标明所述 API 对应的修订；使用其他版本时，先核对差异。实际平台验证范围见验收记录。
