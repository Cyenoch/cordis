---
title: Go API 导航
description: 当前公开入口与职责。
---

# Go API 导航

| 包 | 主要入口 |
|---|---|
| `cordis` | `Open`、`Host.AddBuiltin`、`Install`、`Select`、`Enable`、`Call`、`Emit`、`Close` |
| `contract` | `Definition`、`Entry`、`Scope`、`Instance`、服务与事件类型 |
| `sdk` | `Run`、类型化 `Provide` / `Call`，以及显式流测试入口 `RunTransport` |
| `schema` | 声明解析、配置验证、服务与配置生成、编辑器 schema 导出 |
| `packagehost` | 包构建、签名、验证、安装、选择与运行记录 |
| `runtime/native` | 启动和回收标准协议原生进程 |
| `plugintest` | 真实 SDK、业务服务测试端口与可等待清理 |
| `config` | TOML、YAML、JSON 的严格配置读取 |

## Definition 与 Scope

`Definition.Children` 直接声明 builtin 可用子定义，不需要第二次全局登记。`contract.CreateChild` 为 builtin 和 native 返回子实例句柄。

`Scope` 提供 Identity、Config、Context、Defer、Provide、Call、On、Emit。`contract.Go` 登记显式并发工作；`contract.Own` 接管可关闭资源。

## Instance

实例提供 `Status`、`Wait`、`Update`、`Stop`。Update 返回 revision、Applied、Superseded；取消等待只结束等待。远端子实例的 Status 是最后收到的快照，`Refresh(ctx)` 获取当前状态。

Host 的 `Instance(id)` 和 `Instances()` 返回当前登记。持久控制、恢复和包管理的区别见[宿主接入](host-guide.md)。

## 错误

使用 `errors.Is` / `errors.As` 查看 context 错误、`contract.Error` 和包提交错误。`packagehost.CommitError` 表示提交点已跨过，不应当按“未发生”重复操作。
