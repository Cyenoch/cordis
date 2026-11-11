# Proposal

## Why

项目目标是在 Go 中实现官方 Cordis 的全部可实现能力，按 Go 的思想和方式表达，并做到类型安全。官方 core 的活对象服务、任意深度的实例树、运行期发布、waterfall、intercept 需要一个进程内的类型化组合内核：进程协议只能交换 JSON，不适合作为组合语义的载体，类型安全也不能只是一层编解码包装。

完全体分五个 change，全部已立项。实现与归档严格按下表顺序进行：

| 顺序 | change | 内容 |
|---|---|---|
| 1 | `add-typed-composition-kernel`（本 change） | 进程内类型化内核 |
| 2 | `rebase-native-on-kernel` | Host 与 native 运行时建立在内核上，guest 进程使用同一套 API，仓库里只有一套生命周期实现 |
| 3 | `add-wire-streams-and-chains` | 跨进程的流式结果与中间件链 |
| 4 | `add-config-loader` | 配置文件驱动的插件树、分组、嵌套文件与写回 |
| 5 | `add-logging-and-timers` | 日志服务与定时辅助 |

每个 change 的规格增量都以前一个归档后的主规格为基础。

## What Changes

- 新增进程内的类型化组合内核，位于根包 `github.com/Cyenoch/cordis`，只依赖标准库：
  - 实例树：在作用域 `*Scope` 中加载 `Plugin[C]` 得到 `*Instance[C]`，父停止或重启先清理子树；可派生能单独释放的子作用域。
  - 类型化服务：`Service[T]` 键发布和获取普通 Go 值；`Requires` 声明依赖，依赖齐备才启动，provider 变化时消费者先清理再重启。获取返回 `(T, error)`，没有 panic 路径。依赖可带接受条件。
  - 服务隔离与类型化的作用域设置 `Setting[O]`（对应官方 intercept）。
  - 三种类型化的键：`Event[T]`（顺序、并行分发）、`Query[In, Out]`（首个结果）、`Chain[In, Out]`（中间件链，对应官方 waterfall），支持前置、只触发一次、取消和范围过滤。
  - 资源：`Defer` 逆序清理、受管后台工作、随实例取消的 `context.Context`。
  - 类型化配置更新、就地更新、重启，以及状态、等待、生命周期事件和内核关闭。
- 根包 `github.com/Cyenoch/cordis` 是内核；Host（`Host`、`Open`、`Options`、`Recovery*`、`Service`、`Provide`、`Call`）位于 `github.com/Cyenoch/cordis/hosting`。
- 文档按“进程内内核”和“Host/native”两列给出与官方 Cordis 的对照，提供内核教程和可运行示例；ROADMAP 列出五个阶段。

范围与非目标：

- 本 change 交付内核与 `hosting` 包布局；Host/native 建立在内核上由 `rebase-native-on-kernel` 完成。
- 以下官方能力在 Go 中不提供：Proxy 式 `ctx.foo` 属性访问、mixin、accessor、`ctx.set`、非根作用域上不带声明的宽松查询、可调用服务、按插件索引的 registry、插件从内部停止或更新自己、`internal/dispatch` 钩子、effect 的自省、保留内存状态的 HMR、加载 TypeScript 插件。
- 对齐对象是只读参考提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88` 的 `packages/core`，不承诺跟随官方最新版本。对齐的是能力和语义，不是 API 的形状与名称。

上游观察与框架决定的区分见 design.md 决定 12 的差异表。

## Capabilities

### New Capabilities

- `framework-kernel`：进程内类型化组合内核的实例所有权、服务依赖与隔离、事件与中间件链、资源释放、配置更新、生命周期事件和可观察状态。

### Modified Capabilities

- `framework-host-integration`：“公开能力边界”要求文档区分进程内内核已具备的能力与 Host/native 尚未具备的能力。

## Impact

- 公开 API：根包导出内核；`hosting` 包导出 Host。
- 代码：根包是内核源码与行为测试；`hosting/` 是 Host 源码与测试；`cmd/cordis`、`examples/basic-host`、`conformance/cmd` 导入 `hosting`；`examples/kernel` 是内核示例。
- 依赖：不新增模块依赖，不依赖 Go 1.27 新增的语言特性。
- 规格与文档：新增 `framework-kernel`；修改 `framework-host-integration`。
- 公开契约须经非作者独立审查；验收为 `bash conformance/run.sh`（含 race）及 CI 五平台实际运行。
