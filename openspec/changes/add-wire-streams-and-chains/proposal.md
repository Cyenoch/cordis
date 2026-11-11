# Proposal

## Why

跨进程的端点调用、事件和查询是一问一答。两类需要要在进程边界上满足：逐项产生的结果（模型的逐 token 输出、工具的输出、长任务进度），以及由多个插件依次拦截同一次调用的中间件链。它们是用本框架搭建 agent 类应用的前提。

## What Changes

- 流式服务方法：作者声明可把方法标记为流式结果，生成的方法返回 `iter.Seq2[T, error]`。跨进程时逐项传输，带基于额度的背压；消费者停止迭代即取消发送方。
- 可跨进程的中间件链 `contract.ChainKey[In, Out]`：不同进程登记的中间件组成一条链，`next` 经会话传到下游，操作与内核的 `Chain` 对应。
- 协议有两种帧（流的数据项与额度）和链相关的方法，作为可协商的特性 `streams`、`chains`；协议 major 1、minor 0。不使用这些特性的插件不涉及它们。
- 文档的能力对照：跨进程可用的是端点、事件、查询、流式结果和中间件链。

范围与非目标：

- 只提供由被调用方产生的结果流。由调用方上传的流和双向流不提供：没有具体需求，双向交互可由一个结果流加普通调用组成。
- 事件、查询不流式化。
- 帧预算、在途上限、取消规则由 `wire` 定义；流的每一项各自受帧预算约束。一问一答请求有期限上限；流按“被调用方持有额度时的静默时间”计时，链的执行不设期限上限，由发起方的 context 约束。
- 活对象、函数值和设置仍然不能跨进程；“把处理函数交给另一个插件”通过查询和中间件链的登记完成。

上游观察：官方 Cordis 的 `waterfall` 在单进程内传递 `next`，异步迭代器作为普通返回值使用。跨进程传输、背压和特性协商是本项目的决定。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-execution`：增加流式服务方法、跨进程中间件链和特性协商。
- `framework-host-integration`：“公开能力边界”说明跨进程已具备的能力。该需求的文本以 `rebase-native-on-kernel` 归档后的主规格为基础。

## Impact

- 公开 API：`contract.Endpoint` 有流式调用方法；`contract.ChainKey[In, Out]`；作者声明格式有方法的流式标记；生成代码有返回 `iter.Seq2` 的方法。
- 代码：`wire`（帧种类、额度）、`contract`、`schema`（声明与生成器）、`sdk`、`hosting`、`schema/session.schema.json`、`schema/contract.schema.json`、示例与文档。
- 依赖：不新增模块依赖。
- 公开契约须经非作者独立审查；验收为 `bash conformance/run.sh` 及 CI 五平台实际运行。
