---
title: 会话协议
description: 标准原生进程会话、帧预算和调用取消。
---

# 会话协议

会话采用每行一个 JSON 帧，schema 为 `cordis-go.session/v1`。帧带 activation、方向相关请求 ID、method、payload 和有限期限；取消帧引用已发送的请求 ID。

## 初始化

Host 发送 initialize：identity、protocol、definition、config、frameBytes。SDK 在调用作者代码前验证协议并应用预算；registration 必须原值确认 frameBytes，返回 minor、features、services、subscriptions。

预算范围 4096 到 1048576 字节，包含完整编码帧。Host 默认 1048576。未知必需特性、缺少字段或预算不一致使初始化失败，不会发布服务。

## 方法

| 方法 | 职责 |
|---|---|
| `initialize` | 选择定义并初始化 |
| `activation.publish` | 确认最终发布，允许后台工作启动 |
| `activation.retire` | 取消普通工作，保留清理通道 |
| `call` | 双向服务请求 |
| `event` | 事件分发 |
| `child` | 同包子实例创建与控制 |
| `health` | 查询当前发布状态 |
| `shutdown` | 等待清理 |

普通在途请求上限统一为 64，已取消但尚未退出的请求另有预算。迟到响应不会恢复已取消调用。循环调用通过 parent 关联与等待图明确拒绝。

## 失败隔离

接收到畸形、超大或无法关联的帧会关闭对应会话。发送前发现普通响应过大时，只将当前调用替换为有限错误，后续调用仍可继续。清理、取消和初始化不会因这一替换规则被误报成功。

错误包含 code 与 message，调用者使用错误类别处理。框架不接管应用错误的脱敏或日志策略。

公开结构见 `schema/session.schema.json`；签名与包结构见[包格式](packages.md)。
