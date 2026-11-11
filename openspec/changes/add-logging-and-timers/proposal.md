# Proposal

## Why

官方 Cordis 自带两项几乎每个插件都会用到的能力：按调用方命名、可按名称调级别、可挂接输出目标的日志，以及随插件停止自动取消的定时器。内核提供生命周期自身的可选诊断日志和受管后台工作；按调用方命名、可调级的日志和随实例取消的定时器由这两个包提供，插件作者不必各自实现。

## What Changes

- `logging` 包：一个内核插件，发布日志服务。插件取得的是标准库的 `*slog.Logger`，名称来自调用方实例，可用作用域设置覆盖；支持默认级别、按名称的级别和作用域设置的级别，配置在运行期就地生效，可挂接额外的 `slog.Handler`，有最近记录的环形缓冲和实例生命周期的记录。
- `timer` 包：一组以调用方作用域为参数的函数（`AfterFunc`、`Every`、`Sleep`、`Ticks`、`Throttle`、`Debounce`），提供延时回调、周期回调、可取消的等待、节拍序列、节流和防抖。回调归属调用方实例，实例停止时自动取消。它不是服务，也不需要加载插件。
- 文档说明两者的用法与官方的能力对照。

范围与非目标：

- 日志建立在 `log/slog` 上。不提供官方的 printf 风格格式串、按名称着色和自定义格式化器；需要特定排版的应用自行提供 `slog.Handler`。官方按输出目标配置的级别由 `slog.Handler` 自己的级别判断承担。
- 默认输出写到标准错误。native 插件的标准输出是协议通道，`framework-execution` 规定日志不得混入。
- 不做跨进程的日志汇集：guest 进程内加载的日志插件只管本进程，其标准错误由宿主转发。
- 内核 `Options.Logger` 的诊断日志保持独立，不经过本服务。
- 定时器不提供可替换的时钟服务；测试用标准库的 `testing/synctest` 控制时间。

上游观察：按调用方命名、默认与按名称的级别、输出目标作为可撤销的登记、1000 条的缓冲、定时器随调用方上下文释放、节流的首次与尾随调用，来自官方 `core/src/logger.ts`、`logger-console` 与 `timer` 的源码和测试。基于 `slog`、标准错误、函数式的定时辅助是本项目的决定。

## Capabilities

### New Capabilities

- `framework-logging`：按调用方命名的日志服务、级别、输出目标、最近记录与生命周期记录。
- `framework-timers`：归属调用方实例的延时、周期、等待、节流与防抖。

### Modified Capabilities

（无）

## Impact

- 公开 API：`logging` 与 `timer` 两个包。
- 代码：两个包及其测试、示例、文档。
- 依赖：不新增模块依赖。
- 前置：前四个 change（顺序见 `add-typed-composition-kernel` 的 proposal）；两个包只使用内核的公开 API。
- 公开契约须经非作者独立审查；验收为 `bash conformance/run.sh` 及 CI 五平台实际运行。
