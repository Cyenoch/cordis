---
title: Cordis
description: Go 插件组合与 native 进程执行。
template: splash
hero:
  title: 插件独立交付，应用持续生长。
tableOfContents: false
---

# Cordis

Cordis 是独立的 Go 插件组合框架。插件声明服务与依赖，框架在依赖就绪后启动实例，并在停用时按所有权和依赖顺序释放资源。外部插件通过 native 进程运行，以签名 ZIP 包交付。

项目尚未发布；文档对应当前源码，不提供旧 API 的兼容层。Go module 为 `github.com/Cyenoch/cordis`，MIT 许可。

| 目标 | 入口 |
|---|---|
| 运行第一个 native 插件 | [快速开始](getting-started.md) |
| 理解实例、服务与清理 | [核心概念](concepts.md)、[组合实操](composition-tutorial.md) |
| 编写插件 | [插件作者](plugin-author.md)、[数据契约](contracts.md) |
| 在 Go 应用里加载插件 | [宿主接入](host-guide.md)、[API](api.md) |
| 构建、打包与签名 | [CLI](cli.md)、[包格式](packages.md) |
| 排查运行问题 | [排障](troubleshooting.md)、[生命周期验收](lifecycle-acceptance.md) |

native 进程拥有启动用户的权限，签名证明来源与完整性。资源清理不会撤销已经发生的业务副作用。应用策略通过普通服务实现。
