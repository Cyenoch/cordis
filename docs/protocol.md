---
title: 会话协议
description: Host 与 native 进程之间的帧格式、方法、流与预算。
---

# 会话协议

Host 与 native 进程通过一条会话交换服务调用、事件和中间件链。本页给出线上格式；作者不需要直接接触它，[SDK](plugin-author.md) 和 [Host](host-guide.md) 已经实现了这些规则。

会话在进程的 stdin/stdout 上，每行一个 JSON 帧，schema 为 `cordis-go.session/v1`，协议版本为 major 1、minor 0。stdout 只传协议帧，日志写 stderr。帧的完整结构见 `schema/session.schema.json`，包格式与签名见[包格式](packages.md)。

## 帧

| `kind` | 方向 | 内容 |
|---|---|---|
| `request` | 双向 | `activation`、`id`、`method`、`payload`、`deadlineMs`，可带 `parent`；流请求另带 `stream:true` 与 `window` |
| `response` | 双向 | 回应同一 `id`，带 `payload` 或 `error`（`code` 与 `message`） |
| `cancel` | 双向 | 引用已发送的请求 `id` |
| `item` | 被调方→调用方 | 流的一项结果 |
| `credit` | 调用方→被调方 | 授权被调方再发送若干项 |

请求 ID 为 `h:<序号>`（Host 发出）或 `g:<序号>`（插件发出）。`parent` 引用正在处理的请求，用来识别 A 调 B、B 又回调 A 的环；环以可诊断的 `reentrant_call` 错误拒绝。

## 初始化与关闭

Host 先发 `initialize`，载荷含 `identity`、`protocol`、`definition`、`config`、`frameBytes`、已绑定的 `services`、Host 支持的 `features` 和该定义声明的 `provides`。SDK 在运行作者代码前校验协议并应用帧预算，随后返回 registration：原值确认 `frameBytes`，附上 `minor`、`features` 与已发布的 `services`。声明了却没有发布的服务、未声明的服务、预算不一致、缺少 Host 不支持的必需特性，都使初始化失败；失败的激活不会发布任何服务。

初始化期间插件可以向 Host 发请求（`Start` 内的调用和订阅），它们以 `initialize` 为 `parent`。其余请求在初始化成功后才被接受。

退役时 Host 在消费者清理完成后发送 `shutdown`，SDK 取消普通工作、运行 `Defer` 清理并确认。插件在启动后失败时，先发 `failed` 报告原因，再关闭会话。

## 方法

| 方法 | 方向 | 职责 |
|---|---|---|
| `initialize` | Host→插件 | 选择定义并启动 |
| `shutdown` | Host→插件 | 等待清理 |
| `call` | 双向 | 调用服务的一个方法；可为流 |
| `event` | 双向 | 插件→Host：触发事件或查询；Host→插件：按订阅 ID 投递给一个监听者 |
| `event.subscribe` | 插件→Host | 登记监听者或查询处理者，成功响应 `{}` |
| `event.unsubscribe` | 双向 | 取消登记，响应 `{"inactive":true}`；Host 方向用于释放它不会再投递的订阅 |
| `child` | 插件→Host | 创建同包子实例 |
| `failed` | 插件→Host | 报告启动后的失败 |
| `chain.use` / `chain.unuse` | 插件→Host | 登记或撤销中间件 |
| `chain.invoke` / `chain.final` | Host→插件 | 运行插件的中间件；向发起方索取最终步骤 |
| `chain.do` / `chain.next` | 插件→Host | 由插件发起一次链执行；中间件调用 `next` |

`call` 的载荷：Host→插件为 `{service, method, input}`（`service` 是已注册服务的精确版本），插件→Host 为 `{required, method, input}`（`required` 是声明的版本范围）。Host 按发起实体的绑定解析 provider，范围不含 provider 版本时返回 `incompatible`。

## 特性

Host 在 `initialize` 的 `features` 中列出所支持的特性，包的 `protocol.requiredFeatures` 声明必需项；缺少任一必需特性，包不会获得进程。

| 特性 | 含义 |
|---|---|
| `cancel` | 取消帧 |
| `parent` | 请求的 `parent` 关联与环检测 |
| `events` | 事件与查询的触发 |
| `subscriptions` | 动态订阅与投递 |
| `children` | 同包子实例 |
| `streams` | 流式调用 |
| `chains` | 跨进程中间件链 |

契约里声明了 `stream` 方法时，构建会自动把 `streams` 加入包的必需特性。

## 预算与期限

| 项 | 取值 |
|---|---|
| 帧大小 | 4096 到 1048576 字节，含完整编码帧；默认 1048576，由 `Options.FrameBytes` 设置 |
| 请求期限 | `deadlineMs` 为 1 到 120000；调用方 context 没有期限时取 30 秒，有期限则按剩余时间 |
| 在途请求 | 每个方向 64 个；其中 8 个留给订阅、链登记等控制请求，业务请求最多 56 个并行，更多的排队等待 |
| 取消宽限 | 1 秒内必须回应取消，否则会话以 `timeout` 结束 |
| 活动订阅 | 每次激活最多 256 个（事件、查询与链登记合计），取消或 `once` 消费后释放名额 |

`chain.invoke`、`chain.next`、`chain.do`、`chain.final` 没有自己的期限，随发起者的 context、取消或会话结束。链的每一跳在会话中占一个在途请求直到整次执行返回，因此不排队：没有空位时该跳以 `not_ready` 失败。

畸形、超大或无法关联的帧会结束会话。发送前发现某个普通响应超过帧预算时，只把这次调用替换为有限错误，会话继续；初始化、取消和清理不会因此被误报为成功。迟到的响应不会恢复已取消的调用。

## 流

流请求带 `stream:true` 和 `window`（1 到 256，通过 `Endpoint.Stream` 发起时为 16）。被调方每发一个 `item` 消耗一份额度；调用方消费一半窗口后发 `credit` 补充。没有额度时被调方等待，因此慢消费者不会撑大内存；超出额度的 `item` 使会话失败。流以普通 `response` 结束：成功为 `{}`，失败带错误。调用方提前停止迭代会发 `cancel`，此后到达的 `item` 被丢弃直到响应。

期限只计入调用方在被调方有额度时等待下一项的时间：整个流可以任意长，消费者自己的节奏不计时，但被调方在一个期限内没有任何产出则以 `timeout` 结束。单项超过帧预算只让这一次流失败，不结束会话。

## 订阅与投递

`event.subscribe` 的载荷为 `{"id":"<activation>/<序号>","kind":"event","name":"changed","options":{"once":false,"prepend":false}}`。`kind` 为 `event` 或 `query`；链用 `chain.use`，`kind` 为 `chain`。序号是当前激活内递增、不可复用的正整数；Host 从会话确定归属，其他激活的 ID 被拒绝。

插件→Host 的 `event` 载荷为 `{kind, name, input, mode}`：事件的 `mode` 为 `sequential` 或 `parallel`，查询为 `first`。Host→插件的投递为 `{id, input}`，返回单个 `{handled, value}`；`handled` 必须显式出现，不按 `value` 的真假推断。重复或未知的 ID 投递失败。

取消载荷为 `{id}`。属于当前激活的合法 ID 已经不活动时返回 `inactive`；畸形、其他激活的 ID 或重复登记返回错误。取消阻止后续准入，等待已准入的投递进入会话发送顺序或确定未发送，不等待回调完成，因此回调可以取消自己。`once` 在准入时消费；已消费但投递未能发出时，Host 反向发送 `event.unsubscribe` 释放插件的引用。

错误只携带 `code` 与 `message`，调用方按错误类别处理；框架不接管应用错误的脱敏。
