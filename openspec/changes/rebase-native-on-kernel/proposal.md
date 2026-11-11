# Proposal

## Why

进程内的组合内核（`add-typed-composition-kernel`）提供类型化的实例树、服务与事件；native 插件在独立进程里运行，与宿主之间只有 JSON 会话。本 change 让 Host 和 native 运行时建立在内核上：两种插件用同一套 API、共用一个内核，进程内插件与 native 插件在同一棵实例树上互相依赖。

## What Changes

- Host 持有一个内核：每个 entry 是一个内核实例，entry 的隔离声明是作用域隔离，父子 entry 的所有权用依赖关系表达，持久 entry 保持实例身份。进程内插件直接加载到 Host 的根作用域，与 native entry 共用服务和事件。
- guest 进程运行同一个内核：`sdk.Run` 把作者的 `cordis.Plugin[C]` 加载到 guest 内核，作者使用 `*cordis.Scope`、`Defer`、`Go`、子插件等全部内核能力。
- 可跨进程的服务、事件和查询用 `contract` 包里的键显式表达：服务值是 `contract.Endpoint`，`EventKey` 与 `QueryKey` 在边界上做 JSON 编解码。纯进程内的 Go 类型键不跨进程。
- 生成器从作者声明生成服务接口、端点键、客户端、服务端适配和插件构造函数。
- 会话协议：schema `cordis-go.session/v1`，协议 major 1、minor 0。`initialize` 携带已绑定依赖的版本和宿主特性；`call` 显式携带方法名；事件登记携带种类；事件响应是空对象或 `{handled, value}`。
- native 回调并发执行，并发上限为会话的在途上限，超出的调用排队等待名额；没有调用环检测。
- Host 暴露根作用域；进程内的父由 `Host.ProvideParent` 登记。
- `plugintest` 在内存会话上用真实的 Host 桥接和 guest 运行时。

范围与非目标：

- 包格式、签名、信任、安装与选择、CLI 命令和输出、恢复策略、本机控制由各自的规格定义，本 change 只把它们接到内核的状态上。
- entry 的持久意图、`Restore`、持久 entry 的实例身份与恢复预算、动态子实例的归属与枚举、依赖图校验、运行记录和退出结果由 `framework-composition` 等规格定义。
- native 运行与进程内运行有两处差异：guest 的受管工作可能早于宿主发布其端点启动；宿主侧发布的端点集合在启动完成时固定。
- 进程启动与回收由 `runtime/native` 负责。
- 跨进程的流式结果和中间件链属于 `add-wire-streams-and-chains`。
- 活对象不能跨进程；guest 内的设置、隔离和子插件只在 guest 进程内有效。

上游观察：官方 Cordis 的插件全部在一个进程里，没有对应的进程边界。本 change 的桥接、端点和协议都是本项目的决定。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-composition`：Host 的组合建立在内核上；进程内插件与 native entry 共用实例树；子实例与并发规则。
- `framework-execution`：SDK 入口、生成的契约、协议版本、事件登记与回调并发。
- `framework-host-integration`：“公开能力边界”说明 native 插件在自己进程内可用的能力与跨进程可用的能力。

## Impact

- 公开 API：`contract` 提供数据类型与可跨进程的键；`sdk` 的入口是 `sdk.Run`；`hosting` 提供根作用域与进程内父的入口；生成代码的形状见 design.md。下游 VortexGo 的插件使用这套 API。
- 代码：`hosting`、`sdk`、`contract`、`wire`（负载类型）、`schema`（生成器）、`plugintest`、`cmd/cordis`（`init` 模板）、`examples`、`conformance`、`schema/session.schema.json`、文档。
- 依赖：不新增模块依赖。
- 风险最高的部分是会话生命周期，它由五平台 conformance 和大量 wire、sdk、hosting 测试保护，测试见 design.md。
- 公开契约须经非作者独立审查；验收为 `bash conformance/run.sh` 及 CI 五平台实际运行。
