---
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - session.go
  - host.go
  - children.go
  - contract
  - wire
  - sdk
  - runtime/internal/process/protocol.go
  - schema/session.schema.json
---

# 会话协议

`contract` 是宿主、Runtime 与 SDK 共用的数据契约；`wire` 只处理有界双向会话，不负责领域授权。使用 Go SDK 时不用手写这些帧；本页供其他语言 SDK、Runtime 和宿主协议适配作者查阅。

- UTF-8 NDJSON，stdout 仅协议，stderr 日志；宿主显式指定每帧 4 KiB–1 MiB，默认深度 32、数组 10,000、64 个在途请求。
- `request`/`response`/`cancel`；每个会话绑定一个 activation，宿主请求 ID `h:`、来宾请求 ID `g:`，方向内单调递增且不可重用。
- `initialize` 在业务调用前完成。业务调用使用显式 service/version 和 JSON 数据，不传宿主对象或 Go 指针。
- `parent` 是当前 endpoint 中的请求引用，由 endpoint 和宿主绑定；不接受插件自造的身份链。跨插件调用的等待环由组合内核检测。
- 每次调用都有 deadline；取消先拒绝结果提交并发送 cancel。宽限期内没有回应会关闭会话，Runtime 负责终止和回收执行进程。
- 成功 shutdown 先完整写出响应，再关闭 guest 会话；不能等待下一次 stdin EOF 才退出。响应已匹配后连接关闭仍返回该响应，但调用取消继续优先拒绝提交。
- 一次取消不撤回已发生的外部副作用，也不自动重试业务调用。旧 activation 的消息不能成为新实例状态。
- 协议错误关闭对应会话，错误输出不默认包含原始载荷。宿主 handler 的错误还须经过 `access.Sink.PublicError` 投影。

## 帧格式

每帧是单行 JSON，以 LF 结束。预算计算为 `len(json.Marshal(Frame)) + 1`：完整信封、UTF-8 字节、JSON 转义和末尾 LF 都计入，恰好等于上限可发送。原始 payload 长度、字符数和记录数不能替代最终检查。字段名和 `schema` 必须精确匹配，重复键、未知字段、无效 UTF-8 和不完整末帧均拒绝。

| 字段 | request | response | cancel |
| --- | --- | --- | --- |
| `schema` | 三者均为 `cordis-go.session/v1` | 同左 | 同左 |
| `activation` | 当前会话身份 | 与请求相同 | 与请求相同 |
| `id` | 发起方生成的新 ID | 复用请求 ID | 复用待取消请求 ID |
| `method` | 非空方法名 | 必须与原请求相同 | 不允许出现 |
| `payload` | 必须携带 JSON 数据 | 与 `error` 恰好出现一个 | 不允许出现 |
| `error` | 不允许出现 | `{"code":"...","message":"..."}` | 不允许出现 |
| `deadlineMs` | 必填，1–120000 的整数 | 不允许出现 | 不允许出现 |
| `parent` | 可选，同一 endpoint 的反向请求关联 | 不允许出现 | 不允许出现 |

`deadlineMs` 是接收端从收到请求起计算的剩余毫秒数，不是 Unix 时间。调用者没有 deadline 时，Endpoint 默认补 30 秒；实际超时还受 parent context 约束。activation 为 1–128 UTF-8 字节，ID 最长 128 字节，方法名为 1–256 字节，parent 最长 128 字节。发送端和接收端都检查这些边界，不截断关联字段。

## 帧预算与失败范围

Host 使用 `Options.FrameBytes` 显式选择 `4096..1048576` 的整数，经 `Launch.FrameBytes` 和 Initialize 的必需 `frameBytes` 传递。SDK 在作者 Validate/Start 和初始化反向调用前应用它，Registration 必须原值确认。整个 activation 双向使用同一个值，不允许运行中调整；缺字段、零值、未知字段和不一致确认均失败，不使用旧默认或另一协议重试。

Guest 仅第一条 initialize 用 1 MiB 硬上限有界读取，再按收到的完整原始帧长度复核提议预算；空白也计入。下一帧即使已进入缓冲区也使用绑定预算。Host 从第一帧就使用选定值，不能靠引导上限发送超预算配置。1 MiB 是硬上限，不是省略预算时的默认值。

| 失败 | 调用与会话状态 |
| --- | --- |
| 本地请求在发送前超限 | 只失败本地调用，不执行远端 handler，后续正常调用可继续 |
| 已初始化普通响应在发送前超限 | 原结果不入队；最多替换一次同关联的 `plugin_failed` 小错误，其他调用和 activation 保留 |
| 原错误 message 本身超限 | 同样替换为固定安全说明，不回显原消息，不递归替换 |
| initialize、health、shutdown、activation.publish/retire 或初始化前反向响应超限 | 会话失败，不伪造注册、发布或清理成功 |
| 实际收到超限/坏帧、未知关联、部分写入失败、零进展写入、响应准入背压超时 | 关闭对应会话，不扫描丢弃一行后猜测恢复 |

固定小错误为 `{"code":"plugin_failed","message":"response exceeds frame byte limit"}`。message 不是机器判别或自动重试接口；业务副作用可能已发生，失败不代表回滚。取消/超时仍优先，迟到响应只完成现有取消监督，不重新提交结果。队列准入不等于对端已收到。

分页只减少集合条数；一页一条也可能装不下。作者需按业务契约选择字段投影、摘要、独立读取或显式分块；框架不自动截断 JSON、丢记录、分页、分块或重放调用。最终编码会先分配完整结果，因此帧限额也不是任意大业务对象的内存配额。

以下是初始化完成后的一次 counter 调用。每个代码块在传输中各占一行，并追加换行：

```json
{"schema":"cordis-go.session/v1","kind":"request","activation":"demoactivation","id":"h:2","method":"call","payload":{"cleanup":false,"service":{"name":"example.counter","major":1,"minor":0},"input":{"method":"execute","input":{"operation":"add","delta":2}}},"deadlineMs":30000}
```

```json
{"schema":"cordis-go.session/v1","kind":"response","activation":"demoactivation","id":"h:2","method":"call","payload":{"value":12,"events":0}}
```

取消帧不携带方法或业务数据：

```json
{"schema":"cordis-go.session/v1","kind":"cancel","activation":"demoactivation","id":"h:2"}
```

同一个请求只提交一次结果。响应的 ID 或方法不匹配、重复响应或 activation 错误都会使会话失败；被取消请求的迟到响应按取消监督规则处理，不会交给业务方作为成功结果。

## 方法与方向

同名 `call` 在两个方向使用不同结构，不能交换 envelope。下面的方法属于通用协议；宿主专有 ABI 的方法由宿主定义。

| 方向 | 方法 | 输入与结果 |
| --- | --- | --- |
| Host → guest | `initialize` | `contract.Initialize`：必需 FrameBytes、可信 Identity、Protocol、Definition、Config；Registration 原值确认 FrameBytes |
| Host → guest | `call` | `contract.Invocation`：已解析的精确 Service 与 Input；返回服务的 JSON 结果 |
| Host → guest | `event` | `contract.Event`：Name、Input、Mode；返回 EventResult 数组 |
| Host → guest | `health` | `{}`；SDK 返回 `{"ready":true}`，不能据此替代应用健康检查 |
| Host → guest | `shutdown` | `{}`；成功清理后返回 `{}`，完整写出再关闭会话 |
| Host → guest | `task.cancel` | TaskRequest 的 ID、Token；取消对应 SDK 任务，返回 `{}` |
| guest → Host | `call` | `contract.ServiceCall`：Requirement 与 Input；经当前 Scope 的固定 binding 调用 |
| guest → Host | `event` | `contract.Event`；由当前 Scope 发射并返回 EventResult 数组 |
| guest → Host | `host.invoke` | `contract.HostCall`：Capability 与 Input；经过 Broker 授权 |
| guest → Host | `task.begin` / `task.finish` | `contract.TaskRequest`；始终带 ID 与 Token，完成时再带 State、Result、Failure |
| guest → Host | `child` | `contract.ChildRequest`：Action、局部 ID、Child；按操作返回状态或更新结果 |

Initialize 选择双方支持范围内最高的共同 minor，major 不同或无交集即拒绝。当前 SDK 支持 major 1、minor 0，返回 `cancel`、`parent`、`tasks`、`events`、`children` 特性。Manifest 的 requiredFeatures 必须可协商，且出现在 Registration 中；服务注册还必须与所选 definition 的 Manifest 声明一致。初始化成功前不接受普通业务请求；由合法 initialize / shutdown parent 派生的反向请求可用于初始化和清理中的依赖调用。

任务终态、ID 与 token 的区别见[访问实操](access-tutorial.md)。生成的多方法服务在 Invocation / ServiceCall 的 Input 内使用自己的 `method` / `input` envelope，不增加新的会话级方法，见[生成服务实操](generated-service.md)。

## 接入宿主专有协议

宿主若已有公开协议，使用 `contract.Launch.SessionFactory` 提供 `{Protocol, Open}` 描述符。`Protocol` 明确为 `generic` 或 `custom`，`Open` 接收 transport、Identity、handler 和 frameBytes。nil 明确选择库内通用实现；非 nil 描述符缺字段或未知种类会失败，不凭构造失败切换协议。Facade 的 `Options.SessionFactory` 按已验证 Manifest 选择一次，Host/Runtime 在启动前用同一声明校验。通用 wire 包装器也必须标为 generic 并遵守预算；custom 必须提供 `contract.PluginSession` 映射，不注入通用 envelope、预算握手或额外往返。专有协议的 codec、领域字段和限制由宿主维护。

SDK 的 `Serve`/`ServeTransport` 管理作者侧初始化、服务注册、事件与资源清理。作者的普通处理器串行准入，控制/取消独立读取。自行创建的裸 goroutine 不是受管任务，不能依赖框架替作者回收。

SDK 使用标准 Go goroutine 处理业务、响应和独立控制读取，不要求作者调用 Cooperative/Pump。WASI worker 保留原始 `*os.File` stdin，使 wazero 的非阻塞读取与 `poll_oneoff` 能推进等待中的 goroutine；业务请求等待宿主响应时仍可接收取消和 shutdown。

## 取消与作者子实例

发送准入是可取消的；一个慢写者不使后来的独立 deadline 失效。未响应取消保留到应答或宽限期到期，不能被新取消淘汰；有界历史满且无法安全回收时会话失败关闭。已取消请求的在途反向请求沿原 parent context 被取消，不因合法 parent 的迟到而误杀健康会话；业务 handler 不会在已取消 context 下新准入。

通用 SDK 协商 `children`，通过 `child` 控制方法发送 create/status/wait/update/stop/retry。宿主只使用当前 Scope 身份解析局部 child ID，不信任 guest 自报 owner。子实例控制和服务调用共用等待环检测。专有 Options.HandleGuest 接管全部方法时，须自行明确是否提供该能力，不由框架在专有 ABI 外隐式加 envelope。

通用 `call` 的 Invocation 显式包含 `cleanup` 布尔字段。仅 Host 从已绑定的可信清理 context 设置 true；普通调用为 false。退役后停止普通准入并取消普通 context，已持有绑定的消费者清理调用继续使用独立请求 context 和 parent lineage；这不是 guest 可申请的额外权限。
