# Design

## Context

动机见 proposal.md。前置：前四个 change。

本 change 用到的内核能力都已在 `framework-kernel` 中定义：服务键与依赖声明、作用域设置、`Scope.Info()`、`Defer`、`Go`、`OnUpdate`、`InstanceChanged` 事件，以及“服务方法显式接收调用方作用域”的约定。不需要修改内核。

官方行为的来源是 `third_party/cordis/packages/core/src/logger.ts`、`logger-console` 和 `timer`，提交 `35fdc96`。

约束：`framework-execution` 规定 native 插件的日志不得混入标准输出。

## Goals / Non-Goals

**Goals:**

- 插件作者拿到的就是 `*slog.Logger`，不需要学新的日志 API。
- 级别调整在运行期就地生效。
- 定时回调与实例的停止过程不竞争，不留下游离的 goroutine。
- 两个包都只使用内核的公开 API，可作为“如何写一个服务”的范例。

**Non-Goals:**

- printf 风格格式串、着色、自定义格式化器。
- 跨进程汇集日志。
- 可替换的时钟。

## Decisions

### 1. 日志建立在 `log/slog` 上

```go
// package logging

var Service = cordis.NewService[*Provider]("logger")
var Name = cordis.NewSetting[string](Service)      // 覆盖日志器名称
var Level = cordis.NewSetting[slog.Level](Service) // 覆盖该作用域内的级别

var Plugin = cordis.Plugin[Config]{ /* ... */ }

type Config struct {
    Level  slog.Level            // 默认级别
    Levels map[string]slog.Level // 按名称的级别
    Format string                // "text" 或 "json"
    Buffer int                   // 最近记录的容量，默认 1000
    Output io.Writer             // 默认输出的去向，空表示标准错误
}

func Get(s *cordis.Scope) (*slog.Logger, error)

func (p *Provider) Logger(s *cordis.Scope) *slog.Logger
func (p *Provider) AddHandler(s *cordis.Scope, h slog.Handler) (remove func())
func (p *Provider) Recent() []Record
```

- `Get` 等于 `Service.Get` 加 `Provider.Logger`。插件在 `Requires` 里声明 `logging.Service`。
- 日志器名称：先看 `Name.Values(s)` 的最后一个值，没有则用 `s.Info().Name`。名称作为记录的一个固定属性。
- 级别：先看 `Level.Values(s)` 的最后一个值，没有则查配置里该名称的级别，再没有则用默认级别。作用域设置在取得日志器时读取一次并固定在该日志器上。这对应官方 intercept 的 `name` 与 `level`。
- `Provider` 内部是一个分发用的 `slog.Handler`：先做上述级别判断，再把记录交给默认输出、缓冲和挂接的各个目标。
- “输出目标”就是 `slog.Handler`，对应官方的 exporter。`AddHandler` 在调用方作用域上 `Defer` 移除，因此随调用方实例停止。每个目标自己的 `Enabled` 再做一次级别判断，对应官方按 exporter 配置的级别。
- 某个目标返回错误时，其余目标照常收到记录，写日志的调用不受影响。该错误以一条记录写到默认输出；默认输出自身出错时没有别处可报，错误被丢弃。

备选与否决理由：

- 自定义日志接口（`Info(format, args...)`）：与标准库和生态的 `slog` 不通用，官方的格式串也依赖 JavaScript 的 `util.inspect`。
- 把日志器直接作为服务值（`Service[*slog.Logger]`）：无法按调用方命名，也无法就地调级。

### 2. 级别就地调整

日志插件登记 `OnUpdate`，全部配置都就地应用并返回已应用：

- `Level`、`Levels` 写入一个原子指针，分发处理器每次读取它。
- `Format` 变化时原子地替换默认输出的处理器。
- `Buffer` 变化时按新容量重建缓冲并保留最近的记录。

日志服务几乎被所有插件依赖，重启日志插件等于重启整个应用，所以不走默认重启。已取得的日志器都指向同一个分发处理器，配置变化对它们立即生效。

### 3. 默认输出与最近记录

- 默认输出是写到 `os.Stderr` 的 `slog.NewTextHandler` 或 `slog.NewJSONHandler`。
- 最近记录是固定容量的环形缓冲，保存通过级别过滤的记录的副本（时间、级别、名称、消息、属性）。`Recent` 返回按时间排序的快照。

### 4. 生命周期记录

日志插件监听 `cordis.InstanceChanged`：转换到 Failed 时以错误级别记录，带实例名称和错误；其他转换以调试级别记录。事件负载不含配置，因此记录也不含。

### 5. 定时辅助是函数，不是服务

```go
// package timer

func AfterFunc(s *cordis.Scope, d time.Duration, fn func(context.Context)) (stop func())
func Every(s *cordis.Scope, d time.Duration, fn func(context.Context)) (stop func())
func Sleep(ctx context.Context, d time.Duration) error
func Ticks(ctx context.Context, d time.Duration) iter.Seq[time.Time]
func Throttle[T any](s *cordis.Scope, d time.Duration, fn func(context.Context, T), opts ...ThrottleOption) (call func(T), stop func())
func Debounce[T any](s *cordis.Scope, d time.Duration, fn func(context.Context, T)) (call func(T), stop func())

const NoTrailing ThrottleOption = 1 // 不做窗口结束时的尾随执行
```

官方的定时器是一个服务插件，靠 Proxy 得到调用方上下文。这里调用方作用域是显式参数，没有需要共享的状态，所以用普通函数：不必加载插件，也不必声明依赖。

实现方式：每次登记用 `s.Go` 启动一个受管工作，工作里用标准库的定时器，在定时器、实例的 ctx 和一个私有的停止信号之间 `select`。命名随标准库：带回调的延时叫 `AfterFunc`。

- 受管工作在实例 Active 后启动，因此延时从“登记且实例已 Active”起算。文档写明。
- 实例的 ctx 被取消后回调不再触发；内核等待受管工作结束后才运行清理。按内核的停止顺序，消费者和子实例清理期间 ctx 尚未取消，定时器仍然有效。
- `stop` 只关闭停止信号，不等待，重复调用无副作用。因此回调可以取消自己而不会等待自己。
- 一次性回调触发后受管工作随即结束，内核不再保留任何与它有关的记录，不需要另外释放。
- 回调中的 panic 由内核转换为受管工作的错误，使实例失败。

不使用可单独释放的子作用域：它的释放要等待其中的工作结束，由回调自己释放会成为重入，一次性回调触发后也没有人来释放它。
- `Sleep` 与 `Ticks` 只依赖 ctx。`Ticks` 用容量为 1 的节拍通道，消费者慢时丢弃多余节拍；迭代结束或 ctx 取消时停止定时器。

节流与防抖：

- `Throttle`：窗口内首次调用立即执行；窗口内后续调用只记录最新参数，窗口结束时若有记录则以它再执行一次。带 `NoTrailing` 时不做这次尾随执行，对应官方的 `noTrailing`。
- `Debounce`：每次调用重置计时，静默满 `d` 后以最后一次的参数执行。
- 两者的回调都在受管工作里执行，`call` 只做记录与唤醒；实例停止后 `call` 不产生执行。

### 6. 测试

- 定时相关的测试全部使用 `testing/synctest`，不依赖真实时间。
- 日志测试挂接一个收集记录的 `slog.Handler` 做断言；标准输出的场景用替换的文件描述符验证为空。
- 官方 `logger.spec.ts` 与 `timer` 的测试逐条归类，格式串、着色相关的归为不适用，记录在 `verification.md`。

## Risks / Trade-offs

- 每次登记定时器占用一个 goroutine → 数量与登记数相同，Go 中可接受；文档提示大量定时器应合并为一个周期任务。
- 延时从实例 Active 起算，与“登记即起算”的直觉不同 → 文档写明；在启动函数里需要立即计时的场景可在受管工作里自行计时。
- 分发处理器在每条记录上读取级别表 → 原子读取，开销小；不做基准承诺。
- 输出目标的错误不返回给调用方，可能被忽视 → 写到默认输出，并在文档说明。
- 缓冲保存记录副本会持有属性值的引用 → 容量有界；文档提示不要记录需要及时释放的大对象。

## Open Questions

- 默认文本格式的字段顺序是否需要定制。先用标准库的输出，等有具体的阅读体验反馈再定，不影响规格。
