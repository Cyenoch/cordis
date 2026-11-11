# Design

## Context

动机见 proposal.md。Go 1.27.3。本 change 是完全体计划的第 1 个，其余四个见 proposal.md 的列表。

官方 core（`third_party/cordis/packages/core`，提交 `35fdc96`）的形状：fiber 是递归的 effect 树，服务是任意时刻可发布的活对象，事件钩子表全局共享，`waterfall` 传递 `next`。

Go 实现的约束：

- 服务是进程内的普通 Go 值，运行期随时发布和撤销；实例是任意深度的树。
- 跨进程协议只能交换 JSON，所以组合语义由进程内的内核承载，Host 与 native 建立在内核之上（`rebase-native-on-kernel`）。
- 生命周期的核心算法：先撤销整个依赖闭包、消费者清理完再清理 provider、状态锁不覆盖插件代码、revision 收敛、迟到结果不覆盖新状态。

## Goals / Non-Goals

**Goals:**

- 提供官方 core 的组合能力，但按 Go 的方式表达，不照搬 TypeScript 的 API 形状和名称。
- 内核只依赖标准库，可独立用于纯进程内应用。
- 值类型、配置类型、处理函数签名在编译期受检。运行期只剩四类检查：名称冲突、同名异型、`OnUpdate` 的配置类型、按名称的键。
- 语义以官方 core 的测试为基线，逐条归类为“移植 / 有意差异 / 不适用”。
- 内核 API 在本 change 一次到位：后四个 change 需要的内核能力（条件依赖、按名称的键、生命周期事件、子作用域）都在这里定义，它们只消费内核。

**Non-Goals:**

- 不含进程协议、包管理与 native 激活；它们是 `rebase-native-on-kernel` 的范围。
- 不做性能优化与基准；正确性和可读性优先。

## Decisions

### 1. 按 Go 的方式表达

| 原则 | 落到设计上 |
|---|---|
| 生命周期就是 `context.Context` | `Start` 收到随实例取消的 ctx；停止等于取消加等待；后台工作用 `Scope.Go` |
| 错误是值 | 获取服务返回 `(T, error)`，没有 panic 路径，也没有 `Must` 变体 |
| 显式传递 | 作用域是显式参数 `*Scope`；服务需要知道调用方时，方法显式接收调用方的 `*Scope` |
| 清晰优于巧妙 | 每个操作一个固定签名，不同角色用不同动词，不靠重载或类型推断技巧 |
| 没有魔法 | 不用反射注入、结构体标签、Proxy 式追踪；服务就是普通的 Go 值，推荐用小接口作为服务类型 |
| 沿用仓库已有词汇 | Scope、Instance、Requires、Defer、Go、Middleware、Chain，以及 Waiting、Starting、Active、Quiescing、Stopped、Failed |

官方名称与本项目名称的对应写进文档对照表，不反映到 API 上。

### 2. 分层与包归属

| 包 | 职责 | 允许依赖 |
|---|---|---|
| `cordis`（根） | 进程内组合内核 | 仅标准库 |
| `cordis/hosting` | Host：包管理、native 激活、恢复、控制 | 根包、`contract`、`wire`、`config`、`loader`、`packagehost`、`runtime/native`、`schema` |

依赖方向：`hosting` 及 native 桥接依赖根包，根包不依赖仓库内任何其他包。

包名用 `hosting` 而不是 `host`：`host` 是使用者最常用的变量名，文档片段和 `conformance/cmd/lifecycle-smoke` 都写 `host, err := ...Open(...)`，同名包会被变量遮蔽。

备选与否决理由：

- 内核放子包（如 `cordis/kernel`）、Host 留在根：核心概念藏在子路径下，根包反而是“包管理宿主”。之后再对调会改变全部导入路径。
- 内核与 Host 同住根包：native 桥接需要导入内核，而根包导入 `runtime/native`，会形成导入环；纯进程内使用者也会被迫链接包管理和配置解析。

### 3. 内核自成一套规则

内核的规则是运行期发布、资源树和并发回调。

备选与否决理由：让同一个引擎同时承载活对象和 JSON 处理函数，需要同时保留“封存的注册快照 + 声明式 Provides + 串行槽”和“运行期发布 + 资源树 + 并发回调”两套规则，复杂度高于只保留后一套。协议层的 JSON 处理函数由 `rebase-native-on-kernel` 在内核之上桥接。

### 4. API 形状：类型化的键，操作是键上的方法

键是包级变量，携带类型；操作是键的方法，作用域作为参数传入。`*Scope` 自身只有非泛型方法。

```go
var (
    Audit       = cordis.NewService[*slog.Logger]("audit")
    DB          = cordis.NewService[Database]("db")
    Message     = cordis.NewEvent[Msg]("message")
    ToolExecute = cordis.NewChain[ToolCall, ToolResult]("tools/execute")
)

var SQLite = cordis.Plugin[Config]{
    Name:     "sqlite",
    Requires: []cordis.Requirement{Audit},
    Validate: func(c Config) error { /* ... */ },
    Start:    startSQLite,
}

func startSQLite(ctx context.Context, s *cordis.Scope, cfg Config) error {
    log, err := Audit.Get(s)
    if err != nil {
        return err
    }
    db, err := open(ctx, cfg.DSN)
    if err != nil {
        return err
    }
    s.Defer(func(context.Context) error { return db.Close() })
    if err := DB.Provide(s, db); err != nil {
        return err
    }
    ToolExecute.Use(s, func(ctx context.Context, call ToolCall, next cordis.Handler[ToolCall, ToolResult]) (ToolResult, error) {
        log.Info("tool", "name", call.Name)
        return next(ctx, call)
    })
    return nil
}

app := cordis.New(cordis.Options{})
inst := SQLite.Load(app.Scope(), Config{DSN: "app.db"})
err := inst.Wait(ctx)
err = inst.Update(ctx, Config{DSN: "other.db"})
err = app.Close(ctx)
```

公开面（签名表达设计意图，实现时可微调命名，行为以 spec 为准）：

| 领域 | API |
|---|---|
| 内核 | `New(Options) *App`，`Options{Logger, CapturePanic}`；`App.Scope()`、`Close(ctx) error`、`Done()` |
| 插件 | `Plugin[C]{Name, Requires, Validate, Start}`；`Plugin[C].Load(s, cfg) *Instance[C]` |
| 实例 | `Instance[C].Status()`、`Wait(ctx)`、`Update(ctx, cfg)`、`Restart(ctx)`、`Stop(ctx)`、`Done()` |
| 作用域 | `Scope.Defer(fn) (release)`、`Go(work)`、`Sub() (sub, release)`、`When(requires, start) (release)`、`Isolate(services...)`、`IsolateIn(realm, services...)`、`NewRealm(label)`、`Filter(match)`、`Within(other)`、`Shares(other, service)`、`Info()` |
| 服务 | `NewService[T](name)`；`Service[T].Name()`、`Provide(s, v) error`、`Get(s) (T, error)`、`Where(accept) Requirement`、`Changed(s)` |
| 按名称的键 | `Name("db")`，可用作依赖声明和隔离对象 |
| 设置 | `NewSetting[O](service)`；`Setting[O].Service()`、`With(s, v) *Scope`、`Values(s) []O`、`Default(v) Requirement` |
| 事件 | `NewEvent[T](name)`；`Event[T].On(s, fn, opts...) (stop)`、`Emit(ctx, s, v) error`、`EmitParallel(ctx, s, v) error` |
| 查询 | `NewQuery[In, Out](name)`；`Query.Handle(s, fn, opts...) (stop)`、`Ask(ctx, s, in) (Out, bool, error)` |
| 中间件链 | `NewChain[In, Out](name)`；`Chain.Use(s, mw, opts...) (stop)`、`Do(ctx, s, in, final) (Out, error)` |
| 就地更新 | `OnUpdate(s, fn) error` |
| 生命周期事件 | `InstanceChanged`、`ServiceChanged`（预定义的 `Event`） |
| 登记选项 | `Prepend`、`Once`、`Global` |

选择“键上的方法”的理由已在草稿模块中实测：

- 接收者固定了类型参数，其余参数走普通的可赋值规则。服务键是接口类型、provider 手里是具体类型时，`DB.Provide(s, db)` 直接通过。
- 写成包级泛型函数 `cordis.Provide(s, DB, db)` 或 `*Scope` 上的泛型方法时，`T` 同时从键和值推断，上述情况报 `type sqlite of db does not match inferred type Database for T`，使用者必须显式写类型实参。
- 报错是最普通的形式：`cannot use func(m int) {} as func(context.Context, Msg) error value in argument to Message.On`。
- 不依赖 Go 1.27 新增的泛型方法。`*Scope` 没有泛型方法，`rebase-native-on-kernel` 让 guest 进程运行同一个内核时，调用处不需要任何改动。

带类型的包级变量加方法是 Go 里的常见做法（`expvar`、`flag`、度量库的计数器）。包级函数风格 `Provide(scope, service, value)` 有上面的推断问题，所以不采用。

其他被否决的写法：

- 一个 `On` 通过类型推断同时适配三种处理函数：可行，但报错晦涩，属于技巧。改为三个动词 `On`、`Handle`、`Use`，对应监听者、处理者、中间件三种角色。
- `Defer` 用函数类型并集约束同时接受 `func()`、`func() error`：需要反射归一化具名函数类型。只保留 `func(context.Context) error`。
- 结构体标签加反射注入：名称到类型的对应无法在编译期检查。
- 插件定义为接口加可选接口（按类型断言探测 `Update`、`Validate`）：配置类型写错时断言静默失败。改用带函数字段的描述结构体，与 Go 的 `http.Server` 一类配置结构体同形。

### 5. 服务与依赖

注册表以（解析空间，名称）为键，值以 `any` 存放。键在声明时记录 `T`，绑定依赖时即可检查同名异型，使消费者 Failed 而不是一直 Waiting。

内核不解释服务名，也不做版本协商。版本区间由使用方用条件依赖表达。

`Get` 的准入规则与官方 `locate` 一致：从调用方实例向上逐层查找，每一层先看该实例自己发布的服务，再看其绑定快照与依赖声明，遇到该服务的隔离边界即停止。未被准入时返回错误，说明服务名和缺少的声明。对已准入的服务，`Get` 在实例的整个生命期（含清理）内不会失败。

根作用域属于根实例。根实例始终 Active，是所有实例的祖先；应用可以在根作用域上发布服务、登记处理函数和清理，它们对所有实例准入，并在 `Close` 时撤销。根实例不是插件，无法声明依赖：从根作用域 `Get` 返回当前可用的 provider，不建立依赖关系，因此不保证生命周期；应用代码需要保证时用 `When`。这与官方“没有 fiber 的定义点保留不检查的访问”一致。

服务对其他实例的可见性以 provider 实例进入 Active 为准；实例自己发布的服务自己立即可用。这与官方 `_getImpl(strict)` 一致。

条件依赖：`Service[T].Where(accept func(T) error) Requirement`。`accept` 返回错误时该依赖视为不可用，错误文本作为等待原因。`accept` 在锁外执行，结果在仍然对应同一 provider 时才被采用，并按（消费者，provider）缓存；只有 provider 更换或 provider 调用 `Service[T].Changed(s)` 时才重新评估。因此条件是否随时间变化完全由 provider 决定：从不调用 `Changed` 的条件就是一次性的启动门。等待原因以 error 值保存在状态里，使用方可以用 `errors.As` 取出结构化信息。它对应官方的 `Service.check` 与 `reflect.notify`；使用者是版本区间（`rebase-native-on-kernel`）和 loader 的就绪等待（`add-config-loader`）。

按名称的键：`cordis.Name("db")` 实现依赖声明和隔离对象两个接口，不携带类型，也不做类型检查。使用者是按配置文件工作的 loader。

子作用域：

- `Sub()` 返回一个归属当前实例的子作用域和它的释放函数 `func(context.Context) error`，形状与 `context.WithCancel` 相近。释放按停止规则进行并等待完成，返回清理错误；释放后子作用域失效，重复释放无副作用。在该子作用域自己的回调或受管工作里调用释放时，按重入规则提交请求并立即返回重入错误，释放在回调返回后完成。它覆盖官方 `provide` 返回的撤销函数和组合式 `effect`。
- `When(requires, start)` 是带依赖的子作用域，返回同样形状的释放函数，对应官方的 `ctx.inject`，是可选依赖的做法。需要查询等待原因的使用方应改用一个声明了这些依赖的插件，以取得实例句柄。

### 6. 依赖快照与重启

每个实例维护：

- 每个所需服务当前可用的 provider（已通过条件）。
- 目标标识：已停止、已失败或缺依赖时为不可运行，否则由 revision 与各 provider 的唯一编号组成。
- 本次激活固定的绑定快照；实例作为这些 provider 的消费者登记。

provider 发布、撤销、状态变化或声明条件变化时，通知同一解析空间内声明了该依赖的实例重算目标标识。目标标识与正在运行的不一致时，实例先停止当前激活，再按新目标启动。

### 7. 状态机与并发

- 一把互斥锁保护全部图状态，由唤醒通道驱动的协调循环推进状态；任何插件代码都在锁外、在独立 goroutine 中执行。
- 每个实例同一时刻至多一个在途转换（启动、停止或就地更新）。转换期间目标的变化只记录，转换结束后对账，因此连续更新自然收敛到最新。
- `Start` 可以阻塞；`Load` 立即返回，`Wait` 等待稳定。这与 `exec.Cmd` 的 `Start`、`Wait` 分工相同。
- 每次激活创建一个新的 `*Scope`。上一次激活的作用域在其清理结束后永久失效。

停止分两个阶段，顺序如下：

1. 状态置为 Quiescing，撤回本实例所发布服务的可见性，通知消费者。实例的 ctx、处理函数和受管工作保持可用。
2. 等待消费者完成停止。
3. 按创建逆序停止子实例。
4. 取消实例的 ctx，停止回调准入，等待已进入的回调和受管工作结束。
5. 按登记逆序执行自身清理（包括移除处理函数和发布）。

前三步先于第 4 步，是因为消费者和子实例的清理可能需要本实例仍在工作，例如向它的后台工作发请求来注销自己。子实例可以不声明依赖而使用祖先发布的服务，所以它们不一定出现在消费者里，必须单独先停。官方同样在 dependent 全部卸载后才处置 provider 的 effect，官方测试“keeps provider listeners alive through the final consumer event”保护这一点。代价是第 3 步期间本实例仍被准入的回调可能用到已停止的子实例并得到错误。

各实例的 ctx 互相独立，不从父实例的 ctx 派生；取消的先后完全由上述顺序决定。

实例启动中途需要停止时没有消费者和子实例以外的等待对象，前三步完成后 `Start` 收到取消。

官方对 fiber 顶层的 disposable 是并发执行的。这里采用顺序逆序，与 Go 的 `defer`、`testing.T.Cleanup` 一致，规格也要求按登记逆序执行一次。

清理函数收到的 ctx 不随实例取消，内核不为它设期限；它带有重入标记。

回调并发：内核不为实例串行化回调。服务是普通的 Go 值，方法调用不经过内核，无法串行化；只串行化事件会造成两套规则。处理函数须并发安全，这是 `http.Handler` 一类 Go 处理函数的通常约定。

重入检测：内核传给回调的 ctx 以及 `Start` 收到的 ctx 带有当前调用栈上各实例的标记链，嵌套分发会追加。`Wait`、`Stop`、`Restart`、`Update` 在确实需要等待一个在途转换时，检查标记链上是否有实例位于该转换必须等待的闭包内（目标自身，递归地加上其后代和消费者）。命中时请求照常提交，但等待返回重入错误。目标已稳定时 `Wait` 直接返回，不做检查。传入不带标记的 ctx 时无法检测，文档说明。

### 8. 错误

只有存在真实失败模式的操作返回 error：

| 操作 | 失败方式 |
|---|---|
| `Provide` | 冲突、作用域已失效 |
| `Get` | 未声明、不可用、同名异型、作用域已失效 |
| `Emit`、`EmitParallel`、`Ask`、`Do` | 处理函数的错误，带事件名与所属实例；同名异型 |
| `Update`、`Restart`、`Stop`、`Wait`、`Close` | 校验、已被取代、重入、取消或实例自身的错误 |
| `OnUpdate` | 配置类型与插件不符 |

登记类操作（`On`、`Handle`、`Use`、`Go`、`When`、`Sub`、`Load`）在已取消 ctx 的作用域上不产生副作用，返回的 `stop` 是空操作，`Load` 返回已 Stopped 的句柄，其 `Wait` 返回失效错误。这等价于“登记后立即被撤销”，所以不需要错误返回；`Provide` 本来就有错误返回，因此一并报告失效。`Defer` 在停止期间仍然接受并保证执行；清理阶段结束后登记的清理立即执行，因为资源此时已经取得。

错误的去向：

- 启动错误和清理错误记录在实例的 `Status().Err`，并由对应的 `Wait`、`Stop`、`Update`、`Restart` 返回。带清理错误的重启使实例进入 Failed。
- `Close` 返回本次关闭中的清理错误和关闭时仍为 Failed 的实例的错误，数量以实例数为界。
- 内核调用的全部回调（`Start`、处理函数、受管工作、更新处理、清理、条件）中的 panic 被恢复为带阶段信息的错误，可用 `errors.Is` 识别为 panic。`Options.CapturePanic` 为真时调用栈写入 `Options.Logger`，不进入实例状态。

补充规则：

- 受管工作在它的 ctx 被取消之后返回的任何错误都不算失败，这覆盖 `http.ErrServerClosed` 一类“因停止而返回”的错误。
- 根作用域上的受管工作失败只写日志：根实例不是插件，没有可失败的对象。
- 条件子作用域（`When`）的启动失败算作声明它的实例的失败；子作用域（`Sub`）的清理错误在未被单独释放时由所属实例的停止返回。
- 已经过时的启动或就地更新，其结果（无论成功还是失败）不影响更新后的目标。
- `Restart` 不改变配置 revision，因此不会让正在等待的 `Update` 得到“已被取代”。
- 对已 Stopped 的句柄再次 `Stop` 是空操作，返回同一个错误。

内核定义自己的哨兵错误（失效、冲突、重入、已被取代、未声明、不可用、类型不符、panic），用 `%w` 包装并带实例与服务或事件名，支持 `errors.Is`。不导入 `contract.Error`，以保持依赖方向。

### 9. 事件、查询、中间件链

三种键对应三种角色，各有固定的函数签名：

| 键 | 登记 | 执行 | 官方对应 |
|---|---|---|---|
| `Event[T]` | `On(s, func(ctx, T) error)` | `Emit`：顺序，首个错误停止；`EmitParallel`：并发全部，`errors.Join` 合并 | `emit`、`parallel` |
| `Query[In, Out]` | `Handle(s, func(ctx, In) (Out, bool, error))` | `Ask`：顺序，首个 `ok` 为真的结果返回 | `bail`、`serial` |
| `Chain[In, Out]` | `Use(s, Middleware[In, Out])` | `Do(ctx, s, in, final)`：`next` 至多一次 | `waterfall` |

- Go 没有同步与异步之分，官方的 `serial` 和 `bail` 合为 `Ask`；“已处理”用 comma-ok 表达，不看结果是否为零值。
- `Middleware[In, Out]` 的签名是 `func(ctx, in, next Handler[In, Out]) (Out, error)`，与 gRPC 拦截器同形。
- 登记返回 `stop func()`，幂等，只阻止后续准入，不等待在途回调。
- 处理函数表按名称全局共享，按登记顺序保存，`Prepend` 插到最前。处理函数自登记起即可被调用，直到其实例停止的第 3 步。
- 回调收到的 ctx 在分发方取消或处理者实例进入停止的第 3 步时取消。
- 范围过滤：`s.Filter(match)` 派生作用域，从它发出的分发只到达 `match` 为真的处理者；`match` 收到处理者登记时的作用域，`Scope.Within` 用于判断子树，`Scope.Shares` 用于判断两个作用域对某服务是否处于同一解析空间。带 `Global` 登记的处理者不受过滤。对应官方的 `Context.filter` 与 `{ global: true }`。
- 同名但类型不同的两个键在执行时被发现并返回错误。
- 流式结果不需要额外机制：`Out` 可以是 `iter.Seq2[T, error]`。

生命周期事件是两个预定义的 `Event`：

- `InstanceChanged` 的负载是 `{Instance Info, From, To State, Err error}`，`Info` 为 `{ID, Name, Parent}`。
- `ServiceChanged` 的负载是 `{Service string, Provider Info, Available bool}`，只送达对该服务处于同一解析空间的监听者。

它们由一个独立于协调循环的分发 goroutine 按发生顺序送达，监听者的错误只写日志。监听者可以调用内核操作并等待，因为协调循环不被它占用；慢的监听者只推迟后续事件的送达，不推迟生命周期本身。对应官方的 `internal/status`、`internal/plugin` 和 `internal/service`；使用者是 loader 的状态展示和 logging 的生命周期记录。

### 10. 设置（官方 intercept）

`Setting[O]` 绑定到一个服务，表达“调用方对该服务的作用域内配置”。键的身份是（服务名，`O`）。

- `With(s, v)` 派生作用域，与 `context.WithValue` 同形，但同一键的值会累积。
- `Default(v)` 生成带默认设置的依赖声明，对应官方 `inject: { name: config }`。
- provider 在收到调用方作用域的方法里用 `Values(s)` 读取：最近声明该依赖的实例的默认设置在前，其后是作用域链上由外到内的设置。合并方式由 provider 决定。
- 只有默认设置的查找在该服务的隔离边界处停止；作用域链上的设置不受隔离影响。这与官方 `resolveIntercept` 一致。

### 11. 配置更新

- `Validate` 在提交前执行，失败则实例不变。加载时的初始配置未通过校验时实例直接进入 Failed，`Start` 不被调用。
- `OnUpdate(s, func(ctx, cfg C) (applied bool, err error)) error` 在 `Start` 内登记，对应官方的 `internal/update`。`applied` 为真表示已就地应用，不重启；为假则走默认重启。处理函数返回错误时实例清理并进入 Failed，因为就地应用到一半的状态不可知。
- 就地更新是一种转换，与启动、停止互斥并按 revision 收敛：处理 cfg1 期间到达的 cfg2 在其结束后再处理一次。
- `OnUpdate` 的 `C` 与插件配置类型的一致性在登记时检查并以 error 返回。`*Scope` 不带配置类型参数，这是唯一做不到编译期检查的地方；错误在首次启动时暴露。
- 各状态下的 `Update`：

  | 状态 | 行为 |
  |---|---|
  | Active，已登记更新处理 | 调用更新处理 |
  | Active，未登记 | 停止后以新配置启动 |
  | Starting | 取消当前启动，以新配置重新启动 |
  | Waiting、Failed | 记录新配置并清除错误，条件满足即启动 |
  | Quiescing | 记录新配置，停止完成后按目标对账 |
  | Stopped | 返回失效错误 |

- `Update` 的等待以 revision 为准：本 revision 稳定后返回；被更新的 revision 取代时返回“已被取代”。

### 12. 与官方的有意差异

| 官方行为 | 本项目 | 理由 |
|---|---|---|
| fiber 顶层的 disposable 并发执行 | 顺序逆序 | 与 `defer` 一致，结果确定 |
| provider 变化时，消费者进行中的加载不被取消 | 取消其 ctx | ctx 取消是 Go 表达“不再需要”的方式 |
| 只有 `update` 能恢复 Failed | `Update` 和 `Restart` 都能 | 显式重启是 Failed 的恢复入口，与 Host 的 `Retry` 对称 |
| 根 dispose 等于重启，根仍可用 | `Close` 是终态 | 进程级关闭是 Go 程序的通常需要 |
| 被取代的更新等待者得到最新结果 | 得到“已被取代” | 较早提交的等待者返回 Superseded，结果明确 |
| 加载中收到更新时调用监听而不重启 | 取消并以新配置重新启动 | 更新处理在 `Start` 内登记，启动中途可能尚未登记 |
| Proxy 让服务方法得到调用方上下文 | 服务方法显式接收调用方的 `*Scope` | 显式传递；`associate`、`shadow`、`invoke` 测试不适用 |
| `ctx.set` 就地替换服务值 | 不提供 | Go 消费者持有具体值，替换必须撤销再发布 |
| `ctx.get` 不带声明的宽松查询 | 只有根作用域可以 | 宽松查询拿到的值没有生命周期保证，`When` 是安全的替代 |
| `serial` 与 `bail` 两种模式 | 合为 `Ask` | Go 没有同步与异步之分 |
| 非空返回值即终止 | 显式 `ok` | 零值也可以是有效结果，由处理者显式声明已处理 |
| 按插件索引的 registry | 不提供 | 它服务于 HMR，Go 不能卸载代码 |
| 插件从内部停止或更新自己（`ctx.fiber.dispose()`、`fiber.update()`） | 不提供 | 作用域不暴露所属实例的句柄；由持有句柄的一方或加载器完成 |
| `internal/dispatch` 钩子、effect 的自省 | 不提供 | 没有使用者；状态与等待原因已由 `Status()` 和生命周期事件给出 |

### 13. 测试与文档

- 行为测试放在外部测试包 `cordis_test`，只经公开 API，表驱动。
- 时序相关的测试使用 `testing/synctest`，不使用 sleep。
- 文档中的代码以 `Example` 测试的形式存在，由 `go test` 验证输出。
- 编译期约束用一个编译测试保护：对 `testdata` 中的若干错误用法执行构建并断言失败，对“具体类型发布到接口键”断言成功。它保护的损害是类型约束退化成运行期错误。

实现每个领域前先读对应的官方测试文件，把每个用例归类并记录到 `verification.md`：

| 官方测试 | 处理 |
|---|---|
| `fiber`、`plugin`、`dispose` | 移植到实例测试 |
| `service`、`service-dispose` | 移植到服务测试；撤销单个服务的用例用 `Sub` 表达 |
| `isolate`、`intercept` | 移植 |
| `events`、`update` | 移植，按决定 9、11、12 调整 |
| `associate`、`shadow`、`invoke`、`reflect`、`decorator` | 不适用，由“显式传递调用方作用域”的场景替代 |
| `logger` | 属于 `add-logging-and-timers` |

## Risks / Trade-offs

- 服务值无法收回：provider 停止后，不遵守生命周期的消费者仍可能调用已停止 provider 的值 → 由停止顺序、实例 ctx 和受管工作保证正常路径；文档写明游离 goroutine 的责任在插件。
- 回调不串行，处理函数可能有数据竞争 → 文档明确约定，内核自身的测试全部在 `-race` 下运行。
- 两阶段停止期间 provider 的后台工作仍在运行，停止耗时取决于最慢的消费者和子实例清理 → 等待可取消，状态保持 Quiescing 可见。
- 子实例先于父的回调停止，停止窗口内父的回调可能调用到已停止的子实例 → 这类调用得到错误而不是挂起；文档写明。
- `Get` 每个依赖多三行错误处理 → 这是 Go 的常规写法，换来没有 panic 路径。
- 键是包级变量，名称可能与服务对象的变量混淆 → 文档约定命名，示例统一。
- 条件依赖、按名称的键和生命周期事件的使用者在后面的 change 里 → 它们各有本规格的场景和内核自身的测试；若后续设计改变了需要，以修改 `framework-kernel` 的 change 处理。
- 动态发布使内核无法预知谁会提供什么，互相等待的实例只会一直 Waiting → `Status()` 报告缺少的服务名与原因。

## Open Questions

- 是否给 `Plugin` 增加仅用于诊断的 Provides 元数据，以便报告互相等待的环。等出现实际的排障需求再定，不影响本 change 的规格。
