---
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
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

- UTF-8 NDJSON，stdout 仅协议，stderr 日志；默认每帧 1 MiB、32 层、数组 10,000、64 个在途请求。
- `request`/`response`/`cancel`；每个会话绑定一个 activation，宿主请求 ID `h:`、来宾请求 ID `g:`，方向内单调递增且不可重用。
- `initialize` 在业务调用前完成。业务调用使用显式 service/version 和 JSON 数据，不传宿主对象或 Go 指针。
- `parent` 是当前 endpoint 中的请求引用，由 endpoint 和宿主绑定；不接受插件自造的身份链。跨插件调用的等待环由组合内核检测。
- 每次调用都有 deadline；取消先拒绝结果提交并发送 cancel。宽限期内没有回应会关闭会话，Runtime 负责终止和回收执行进程。
- 成功 shutdown 先完整写出响应，再关闭 guest 会话；不能等待下一次 stdin EOF 才退出。响应已匹配后连接关闭仍返回该响应，但调用取消继续优先拒绝提交。
- 一次取消不撤回已发生的外部副作用，也不自动重试业务调用。旧 activation 的消息不能成为新实例状态。
- 协议错误关闭对应会话，错误输出不默认包含原始载荷。宿主 handler 的错误还须经过 `access.Sink.PublicError` 投影。

## 帧格式

每帧是单行 JSON，以换行结束；默认 1 MiB 上限包含换行。字段名和 `schema` 必须精确匹配，重复键、未知字段、无效 UTF-8 和不完整末帧均拒绝。

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

`deadlineMs` 是接收端从收到请求起计算的剩余毫秒数，不是 Unix 时间。调用者没有 deadline 时，Endpoint 默认补 30 秒；实际超时还受 parent context 约束。ID 最长 128 字节，方法名最长 256 字节，parent 最长 128 字节。

以下是初始化完成后的一次 counter 调用。每个代码块在传输中各占一行，并追加换行：

```json
{"schema":"cordis-go.session/v1","kind":"request","activation":"demoactivation","id":"h:2","method":"call","payload":{"service":{"name":"example.counter","major":1,"minor":0},"input":{"operation":"add","delta":2}},"deadlineMs":30000}
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
| Host → guest | `initialize` | `contract.Initialize`：可信 Identity、Protocol、Definition、Config；返回 Registration |
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

宿主若已有公开协议，使用 `contract.Launch.OpenSession` 提供会话构造器，不在原协议外再包一层通用 envelope。Facade 的 `Options.SessionFactory` 按已经验证的 Manifest 选择构造器，`Options.HandleGuest` 接收经过 Scope 生命周期与 policy 准入的 guest 请求。返回的 session 若实现 `contract.PluginSession`，Facade 和 Runtime wrapper 保留其 Initialize/Invoke/Deliver/Shutdown 语义。专有协议的 codec、领域字段与兼容性验收由宿主维护，不随 Cordis 发布。

SDK 的 `Serve`/`ServeTransport` 管理作者侧初始化、服务注册、事件与资源清理。作者的普通处理器串行准入，控制/取消独立读取。自行创建的裸 goroutine 不是受管任务，不能依赖框架替作者回收。

SDK 使用标准 Go goroutine 处理业务、响应和独立控制读取，不要求作者调用 Cooperative/Pump。WASI worker 保留原始 `*os.File` stdin，使 wazero 的非阻塞读取与 `poll_oneoff` 能推进等待中的 goroutine；业务请求等待宿主响应时仍可接收取消和 shutdown。

## 取消与作者子实例

发送准入是可取消的；一个慢写者不使后来的独立 deadline 失效。未响应取消保留到应答或宽限期到期，不能被新取消淘汰；有界历史满且无法安全回收时会话失败关闭。已取消请求的在途反向请求沿原 parent context 被取消，不因合法 parent 的迟到而误杀健康会话；业务 handler 不会在已取消 context 下新准入。

通用 SDK 协商 `children`，通过 `child` 控制方法发送 create/status/wait/update/stop/retry。宿主只使用当前 Scope 身份解析局部 child ID，不信任 guest 自报 owner。子实例控制和服务调用共用等待环检测。专有 Options.HandleGuest 接管全部方法时，须自行明确是否提供该能力，不由框架在专有 ABI 外隐式加 envelope。
