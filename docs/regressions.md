---
title: 行为回归
description: 当前测试保护的可观察契约及其入口。
---

# 行为回归

每个契约由哪个包的测试保护：

| 契约 | 测试入口 |
|---|---|
| 依赖就绪、更新、重启、传递清理、服务隔离与事件 | 根包 `cordis` 的 `*_test.go` |
| 配置文件加载、写回、嵌套文件与补丁 | `loader` |
| 日志服务与定时辅助 | `logging`、`timer` |
| 父子身份、动态子实例、native 与进程内插件互相依赖、链与流 | `hosting` |
| 真实启动、取消与清理 | `sdk`、`runtime/native` |
| 完整性、签名、原子选择与进程中断 | `packagehost` |
| 帧预算、在途准入、流的额度与取消 | `wire` |
| 数据契约、编解码与生成客户端 | `contract`、`schema`、`config`、`plugintest` |
| CLI、开发循环与本机控制 | `cmd/cordis` |
| 文档中的完整程序 | `conformance`、`examples/pluginmeta` |

负向断言只保护仍然有效的约束，例如失败不提交配置、取消不返回迟到的成功、重复 provider 不覆盖现有服务。
