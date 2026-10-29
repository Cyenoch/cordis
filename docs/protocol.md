---
title: 会话协议
description: 标准原生进程会话、帧预算和调用取消。
---

# 会话协议

会话采用每行一个 JSON 帧，schema 为 `cordis-go.session/v1`。帧带 activation、方向相关请求 ID、method、payload 和有限期限；取消帧引用已发送的请求 ID。

## 初始化

Host 发送 initialize：identity、protocol、definition、config、frameBytes。SDK 在调用作者代码前验证协议并应用预算；registration 必须原值确认 frameBytes，返回 minor、features、services。订阅使用 `subscriptions` 能力及独立控制消息，初始化期间也由 Host 确认。

预算范围 4096 到 1048576 字节，包含完整编码帧。Host 默认 1048576。未知必需特性、缺少字段或预算不一致使初始化失败，不会发布服务。

## 方法

| 方法 | 职责 |
|---|---|
| `initialize` | 选择定义并初始化 |
| `activation.publish` | 确认最终发布，允许后台工作启动 |
| `activation.retire` | 取消普通工作，保留清理通道 |
| `call` | 双向服务请求 |
| `event` | Guest→Host 广播；Host→Guest 按订阅 ID 投递一个监听者 |
| `event.subscribe` | Guest→Host 登记监听者，成功响应 `{}` |
| `event.unsubscribe` | 取消登记并确认 `{"inactive":true}` |
| `child` | 同包子实例创建与控制 |
| `health` | 查询当前发布状态 |
| `shutdown` | 等待清理 |

普通在途请求上限统一为 64，已取消但尚未退出的请求另有预算。迟到响应不会恢复已取消调用。循环调用通过 parent 关联与等待图明确拒绝。

## 动态订阅

登记 payload 为 `{"id":"<activation>/<sequence>","name":"changed","options":{"once":false,"prepend":false}}`。sequence 是当前 activation 内递增、不可复用的正 uint64 十进制数；options 可省略，两个布尔项默认 false。Host 从会话确定归属，不能用其他 activation 的 ID。每次激活最多 256 个活动订阅，取消或 once 准入立即释放名额。

Guest→Host 的 event 保留 `{name,input,mode}`。Host→Guest 使用 `{id,input}`，返回单个 `{handled,value}`；handled 必须显式存在，不能按 value 的真假判断处理结果。重复或未知 ID 投递失败。

取消 payload 为 `{id}`。格式合法且属于当前 activation 的非活动 ID 返回 inactive；畸形、另一激活的 ID 或重复登记返回错误。初始化订阅在最终发布后才准入，退役后拒绝新增。

Host 在取得实例业务槽后决定准入及 once 消费。取消阻止后续准入，等待已准入投递进入会话发送顺序或确定未发送，不等待回调完成。SDK 在读循环推进到取消响应前保存已收到投递的回调引用，执行仍走原业务槽。登记与取消控制不占业务槽，因此回调可以取消自身。

once 已消费但投递未发送时，Host 反向发送 `event.unsubscribe` 释放 SDK 引用；清理使用现有 grace，无法确认则结束失败激活。SDK 对已发送但结果不确定的登记/取消终止会话，避免留下远端登记；明确拒绝或确定未发送不需要结束会话。取消确认不撤销已准入回调，也不重放业务调用。

## 失败隔离

接收到畸形、超大或无法关联的帧会关闭对应会话。发送前发现普通响应过大时，只将当前调用替换为有限错误，后续调用仍可继续。清理、取消和初始化不会因这一替换规则被误报成功。

错误包含 code 与 message，调用者使用错误类别处理。框架不接管应用错误的脱敏或日志策略。

公开结构见 `schema/session.schema.json`；签名与包结构见[包格式](packages.md)。
