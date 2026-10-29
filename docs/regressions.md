---
title: 行为回归
description: 现有测试保护的当前可观察契约。
---

# 行为回归

| 契约 | 测试入口 |
|---|---|
| 依赖就绪、更新、传递清理和迟到初始化 | `internal/core` |
| 父子身份、配置与动态登记 | 根包的 children、child_controls、host_lifecycle 测试 |
| 真实启动、发布确认、取消和清理 | `sdk` 与 `runtime` |
| 完整性、签名、原子选择和进程中断 | `packagehost` |
| 帧预算、在途准入与取消 | `wire` |
| 配置数据边界、生成客户端与服务端 | `schema`、`config`、`plugintest` |
| 文档中的完整程序 | `conformance`、`examples/pluginmeta` |

删除的能力不再保留“旧接口已经消失”测试。负向断言只保护仍有效的约束，例如失败不提交配置、取消不返回迟到成功和重复 provider 不覆盖现有服务。
