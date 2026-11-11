# Design

## Context

动机见 proposal.md。前置：`add-typed-composition-kernel`：根包是内核，Host 在 `hosting`。

问题与约束：

- native 插件在 guest 进程里运行，与宿主之间只有 JSON 会话。要让进程内插件与 native 插件处于同一棵实例树并互相依赖，宿主与 guest 各运行一个内核，由桥接连接。
- 协议：每行一个 JSON 帧，请求、响应、取消三种帧；方法为 `initialize`、`shutdown`、`call`、`event`、`event.subscribe`、`event.unsubscribe`、`child`。`parent` 字段把反向调用关联到在途请求，并传递期限和取消。
- 一个服务对应一个端点，方法由 `{"method","input"}` 信封复用；生成器从作者声明产出服务接口、端点、客户端和插件构造函数。
- 帧格式、帧预算、在途上限、取消与期限、进程启动回收、包与签名由规格和五平台验收固定，是本 change 的前提。

## Goals / Non-Goals

**Goals:**

- 仓库里只有一套生命周期实现。
- 插件作者只学一套 API；同一个插件既能进程内加载，也能打包成 native 运行。
- 跨进程的边界显式可见：什么能跨进程由键的类型决定。
- 协议只含必要的方法：内核的停止顺序保证消费者清理期间 provider 仍可用，不需要串行槽和清理特权。

**Non-Goals:**

- 帧格式、预算协商、请求编号、取消与期限的规则由 `wire` 定义，不在本 change 范围。
- 不让活对象、设置或隔离跨进程。
- 不做流式结果和跨进程中间件链（`add-wire-streams-and-chains`）。
- 不涉及包管理、信任与 CLI 的行为。

## Decisions

### 1. 每个进程一个内核，由桥接连接

| 位置 | 内核 | 桥接的职责 |
|---|---|---|
| 宿主进程 | Host 持有的 `cordis.App` | 每个 entry 是一个实例；它的 `Start` 启动进程、完成初始化、把 guest 提供的端点发布到宿主内核 |
| guest 进程 | `sdk.Run` 创建的 `cordis.App` | 把宿主的依赖端点发布到 guest 根作用域，加载作者的插件，把它提供的端点登记给宿主 |

激活与进程一一对应：宿主侧实例每次启动对应一个新进程。

备选与否决理由：

- 只把宿主侧接到内核，guest 另写一套生命周期：作者要学两套 API，生命周期实现要维护两份。
- 让 guest 的依赖随宿主动态增减而不重启进程：违反“每次激活独占进程”的规则，并需要一套动态导入协议。保持由宿主侧实例的依赖声明驱动重启。

### 2. 包职责与依赖方向

| 包 | 职责 | 依赖 |
|---|---|---|
| `cordis` | 内核 | 标准库 |
| `contract` | 数据类型（Manifest、Entry、Identity、Status、Outcome、Error、协议负载）和可跨进程的键 | `cordis` |
| `wire` | 帧与会话 | `contract` |
| `sdk` | guest 运行时 | `cordis`、`contract`、`wire` |
| `runtime/native` | 进程启动与回收 | `contract`、`wire` |
| `hosting` | Host：包、entry、恢复、控制、宿主侧桥接 | 以上各包、`packagehost`、`schema` |
| `plugintest` | 内存会话上的测试辅助 | `hosting`、`sdk` |

`contract` 只含数据类型与可跨进程的键，不含行为接口。

### 3. 可跨进程的键

```go
// package contract

type Endpoint interface {
    Service() Service // 名称、major、minor
    Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error)
}

func EndpointKey(name string, major uint32) cordis.Service[Endpoint]
func Require(key cordis.Service[Endpoint], minMinor, maxMinor uint32) cordis.Requirement

type EventKey[T any] struct{ /* ... */ }
func NewEventKey[T any](name string) EventKey[T]
func (k EventKey[T]) On(ctx context.Context, s *cordis.Scope, fn func(context.Context, T) error, opts ...cordis.ListenOption) (cancel func(context.Context) error, err error)
func (k EventKey[T]) Emit(ctx context.Context, s *cordis.Scope, v T) error
func (k EventKey[T]) EmitParallel(ctx context.Context, s *cordis.Scope, v T) error

type QueryKey[In, Out any] struct{ /* ... */ }
// Handle 与 Ask，形状同上
```

- 可跨进程的服务就是值类型为 `Endpoint` 的内核服务，键名为 `name@major`。版本区间用内核的条件依赖表达：`Require` 的条件检查端点的 minor，不满足时消费者 Waiting，等待原因是一个带有可见服务版本的错误值，`hosting` 用它填充等待原因。
- `EventKey[T]` 与 `QueryKey[In, Out]` 的操作与内核的 `Event`、`Query` 一一对应，内部是负载为 `json.RawMessage` 的内核键加 `Encode`/`DecodeData`。区别是登记与取消带 ctx 并返回错误：在 guest 进程里它们是一次可能失败的往返，规格要求取消订阅的失败必须返回。
- 名称与 `EndpointKey` 保持一致；`contract.Event` 是协议负载的结构体。
- 事件、查询、中间件链按种类各有独立的名称空间。
- 这些键在作用域里查找上行链路服务：guest 运行时把它发布在 guest 的根作用域，内核的“根作用域发布的服务对所有实例准入”让任何插件都能取到。取到时操作经会话交给宿主；取不到时直接作用于本进程内核。使用者的代码不区分两种情况。

备选与否决理由：

- 让内核为每个键携带编解码器并在两种表示之间自动转换：内核会依赖 JSON 规则，并且“什么能跨进程”变成隐式的。
- 所有服务都经端点：纯进程内的服务失去活对象和编译期类型。

### 4. 生成的代码

以服务 `example.counter` v1、定义 `counter` 为例：

```go
type Counter interface {
    Execute(context.Context, ExecuteRequest) (ExecuteResponse, error)
}

var CounterEndpoint = contract.EndpointKey("example.counter", 1)

func NewCounterEndpoint(impl Counter) contract.Endpoint      // 服务端适配
func NewCounterClient(ep contract.Endpoint) (Counter, error) // 客户端
func CounterRequirement(minMinor, maxMinor uint32) cordis.Requirement

type CounterConfig struct{ /* ... */ }
func CounterPlugin(start func(context.Context, *cordis.Scope, CounterConfig) error) cordis.Plugin[CounterConfig]
```

- 客户端和服务端实现同一个接口 `Counter`，使用方只依赖接口。
- `NewCounterClient` 检查方法指纹；收到的端点是本进程的服务端适配时直接返回其实现，跳过编解码，但仍执行输入校验，保证两种路径结果一致。
- `CounterPlugin` 填好 `Name`、来自声明的 `Requires` 和基于 schema 的 `Validate`。
- `sdk.Define(plugin)` 是泛型函数，把 `cordis.Plugin[C]` 包成 `sdk.Run` 接受的定义：用 `contract.DecodeData` 把原始配置解码为 `C`。

### 5. 宿主侧的 entry 实例

`hosting` 内部用一个插件表达 native entry。它的 `Requires` 来自 manifest 的依赖声明，`Start` 依次：

1. 记录运行开始，取包快照，启动进程，建立会话。
2. 发送 `initialize`，等待登记结果；核对帧预算、协议版本，以及登记的服务与 manifest 的 `provides` 完全一致。
3. 为每个登记的服务在自己的作用域上发布一个转发到 guest 的端点。
4. 用受管工作监视会话结束；会话意外结束即返回错误，使实例 Failed。
5. 登记清理：发送 `shutdown`、关闭会话、记录运行结束与退出结果。
6. 在自己的作用域上发布父标识服务，使以它为父的持久 entry 可以启动。

guest 的反向请求由桥接在该实例的作用域上执行：`call` 用 `Get` 取依赖端点后调用；事件与子实例请求同理。

entry 与内核的对应：

| entry 概念 | 内核 |
|---|---|
| 持久 entry | 根实例下的一个实例，加载在 `hosting` 为它派生的作用域上；它在整个登记期间是同一个实例 |
| `Isolate` | 该作用域叠加了自身及各级父 entry 的隔离声明 |
| 父子 entry | 父实例在自己的作用域上发布一个按父标识命名的私有服务；子 entry 把它声明为依赖 |
| 动态子实例 | SDK 的创建请求在父实例的作用域下加载一个不持久化的子实例 |
| 进程内父 | `Host.ProvideParent(s, id)` 在进程内插件的作用域上发布同一种私有服务 |
| `Host.Call`、`Caller(entryID)` | 从根作用域或该 entry 的派生作用域 `Get` 端点后调用 |
| `Host.Emit` | 可跨进程事件或查询在根作用域上的分发 |

父子关系用依赖表达而不是用内核的父子实例，原因是持久 entry 必须保持实例身份：

- 父停止时子作为消费者先清理；父重新 Active 后子重新启动。这满足“停止父实例先清理子实例”和“子等待父就绪”。
- 子仍是同一个实例。内核的 Failed 不因依赖变化而恢复，所以父重启不会让失败的子重试；宿主持有的句柄、revision 和恢复预算都保持不变。
- 若改为“父每次启动时加载子 entry”，子会成为新实例：失败状态和恢复预算被重置，违反 `framework-recovery` 的有限重试和“默认不自动重启失败实例”，`hosting` 还需要为“当前没有实例的 entry”另造一套状态。

动态子实例本来就随父的激活创建和结束，使用内核的父子实例。

依赖环：内核只在运行期看到依赖是否满足，无法预知谁会发布什么。图校验所需的信息（manifest 声明的 requires、provides 与父子关系）都在 `hosting`，因此环检测留在 `hosting`，在启用与恢复时执行，结果是 Failed 状态与诊断。

`contract.Status`：

- 状态名与内核一致。
- `Desired`/`Applied` 由 `hosting` 按 entry 计数；内核的“已被取代”错误映射为 `Superseded` 结果标志。
- 等待原因来自内核的缺失服务及其原因：父标识服务对应“父尚未就绪”，版本条件的错误值给出可见的服务版本。
- panic 的栈采集由内核的 `Options.CapturePanic` 承担，`hosting` 把自己的同名选项传给内核。

临时停止用“运行门”表达：每个持久 entry 依赖宿主提供的私有服务 `cordis.host/run:<id>`，撤销它即临时停止，entry 保持同一个实例；停止一个 entry 同时停止它之下的持久 entry，各自需要单独恢复。

恢复循环使用内核的激活次数确认“仍是同一次失败”，在 Host 的操作准入内调用 `Restart`。内核的 Failed 不会因依赖变化自行恢复，所以准入内的检查与重启之间没有其他转换。

### 6. guest 运行时

`sdk.Run`：

1. 建立会话，等待 `initialize`。
2. 创建内核；在根作用域发布上行链路和每个依赖端点的转发端点。
3. 解码配置，加载选中的定义，等待其稳定。
4. 从根作用域取出每个声明提供的端点，缺少则失败；回复登记结果。未声明的端点留在 guest 进程内，不登记给宿主。
5. 之后把宿主的 `call` 交给对应端点，把事件投递交给对应的处理函数。
6. 收到 `shutdown` 时不再接受新的 `call` 和投递，等在途的处理结束后关闭内核，清理完成后回复并结束。宿主侧的消费者此时已按内核顺序停止，仍在途的只可能是不建立依赖关系的根作用域调用。

guest 插件运行期失败（例如受管工作出错）时，guest 以错误结束会话，宿主侧的监视工作使实例 Failed。

与进程内运行的两处差异，文档写明：

- guest 的受管工作在 guest 内的插件 Active 后启动，这早于宿主核对登记结果并发布其端点。宿主核对失败时会话被关闭，进程结束。SDK 在执行启动函数之前自行核对帧预算和协议，所以“预算不一致时不启动受管工作”的场景对使用 SDK 的插件成立。
- 宿主侧发布的端点集合在启动完成时固定。之后在 guest 内新发布的端点不会出现在宿主；撤销已登记的端点后，对它的调用得到不可用错误。

`sdk` 不自带生命周期实现，状态、清理与受管工作由内核提供。`sdk.CreateChild(ctx, s, …)` 经上行链路请求宿主。

### 7. 协议负载

| 方法 | 负载 |
|---|---|
| `initialize` | 请求携带已绑定的各依赖服务的版本、宿主支持的特性和宿主的协议范围；应答是 guest 提供的端点的登记结果 |
| `shutdown` | 宿主请求 guest 清理并结束 |
| `call` | 负载显式携带 `method`，没有清理标记 |
| `event.subscribe` | 携带 `kind`（事件或查询） |
| `event.unsubscribe` | 取消订阅 |
| `event`（guest 到宿主） | 响应为空对象或 `{handled, value}` |
| `child` | guest 请求创建动态子实例 |

协议方法只有上表所列。guest 内核在插件 Active 后自行启动受管工作；内核的两阶段停止保证消费者清理完之前 provider 仍可用，之后直接 `shutdown`。

没有清理特权：guest 的清理发生在宿主侧实例的清理阶段，此时它绑定的 provider 按内核规则仍然可用，普通调用即可。

schema 是 `cordis-go.session/v1`，协议 major 为 1、minor 为 0。必需特性白名单与负载一致。

### 8. 并发

处理函数并发执行，上限是会话的在途请求上限 64。这与内核“处理函数须并发安全”的约定一致。

会话在达到在途上限时，发送方立即返回错误，接收方把超限视为协议错误。并发执行会碰到上限，所以两个方向的转发端点和事件转发在发送前先取得按会话计数的名额，名额不足时等待，ctx 取消时返回取消错误。

没有串行槽，所以调用环（A 调 B、B 再调 A）不会阻塞，也没有调用环检测。生命周期等待的重入检测由内核提供。

### 9. 测试

| 范围 | 内容 |
|---|---|
| `wire/*` | 帧、预算、在途、取消、背压 |
| `sdk` | 激活流程（`initialize` 之后启动，清理发生在 `shutdown`）、订阅、事件分发 |
| `hosting` | 生命周期、子实例、管理、恢复、重试；“消费者清理期间 provider 仍可用”的端到端测试 |
| 集成测试与 `conformance/run.sh` | 插件源码使用同一套 API |
| `runtime/testdata` | 订阅与故障注入的测试插件 |
| `examples/plugins/counter` | 经 `plugintest` 的真实桥接运行，含订阅端口 |

## Risks / Trade-offs

- 会话生命周期是全仓库最细致的部分，易出现回归 → 帧层由 `wire` 的测试单独保护；每条会话生命周期规则有端到端测试；五平台 conformance 必须实际运行。
- 并发回调暴露插件里的数据竞争 → 文档单列；示例与模板在 `-race` 下运行；作者需要互斥时自行加锁。
- 同进程直连跳过编解码，可能与跨进程路径行为不一致 → 直连仍执行同一套输入校验，并有对照测试。
- 上行链路靠“取不到就走本地”分流，取错会静默走错路径 → 该键只在 `contract` 内部使用，guest 运行时在加载任何插件之前发布它，并有 guest 与宿主两种环境的测试。
- 父子关系改由私有服务表达，父标识服务的名称可能与使用者的服务名冲突 → 使用保留前缀，并在启用时拒绝使用该前缀的服务声明。
- guest 的受管工作早于宿主发布端点启动 → 在文档中作为差异写明；宿主核对失败时进程被结束。
- 并发调用排队等待名额，可能掩盖过载 → 等待受调用方 ctx 约束；名额耗尽的次数记入诊断日志。

## Open Questions

- `plugintest` 是否还需要独立的包，还是并入 `sdk` 的测试辅助。按代码量决定，不影响规格。
