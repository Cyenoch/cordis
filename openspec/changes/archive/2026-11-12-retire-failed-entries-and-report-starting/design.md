## Context

源码事实（`a89d1a8`）：

- 内核 `cordis.Instance.Stop` 返回实例结束时的错误："清理错误，或它已处于的失败"。Failed 实例在进入 Failed 前已停掉激活并跑完清理（`node.drive`：`desiredLocked` 在 `n.err != nil` 时不再给出绑定，先 `stop(act)` 再置 Failed），之后 `n.act == nil`。从 Failed 停止只经 `finishLocked` 置 Stopped，`n.err` 不变，所以 `Stop` 返回的正是 `Status().Err` 那个错误值。Failed 只有 `Restart`/`Update` 能离开（`n.err = nil`）。
- `hostInstance.retire`（持久 entry 的永久停止，`Disable`、`Remove`、`Replace` 经 `Host.retireOne` 调用）直接返回内核 `Stop` 的错误，注释写明"让调用方知道它删掉的东西从没正常过"。`disableEntries` 遇到错误且该 entry 不再登记时继续，但把错误带回；`Remove` 收到 `disable` 的任何错误都提前返回，不删持久记录。
- `hostInstance.halt`（`Instance.Stop`）：动态子实例直接返回内核 `Stop`；持久 entry 撤下运行门，在撤下前取 `kernel.Err` 作为返回值，`instance.Wait` 的结果被丢弃。持久 entry 撤门时若清理失败，内核实例随后进入 Failed（`stop` 把清理错误并入 `n.err`），这个错误从未返回给调用方。
- `hostInstance.Status`：持久 entry 依赖内部服务 `runKey(id)`（运行门），等待原因列表把它过滤掉。`Enable` 先 `open()` 加载运行门再加载 entry，两者都异步启动；内核实例初始状态即 Waiting。运行门尚未提供 `runKey`、或 entry 的驱动尚未运行、或依赖条件尚在评估时，内核报 Waiting，`Missing` 只有 `runKey` 或为空，宿主报 Waiting 且原因为空。
- `Host.Close` 返回内核 `App.Close` 的错误，其中包含关闭时仍为 Failed 的实例的错误；`framework-kernel`"内核关闭"与 `framework-composition`"关闭完成与清理错误分离"都明确要求这一点。

## Goals / Non-Goals

**Goals:**

- 停止的返回值只说明这次停止，不复述历史失败；历史失败经 `Status.Outcome` 与运行历史可见。
- 停止过程中真实发生的错误一个不漏地返回，包括此前被临时 `Stop` 丢掉的清理失败。
- `Waiting` 总带原因；启动中的窗口报 `Starting`。

**Non-Goals:**

- 不改内核 `cordis` 包的 `Instance.Stop`、`Instance.Status` 与 `App.Close`。
- 不改 `Host.Close`、配置加载器 `package:` 句柄的 `Stop`。
- 不新增等待原因种类，不改 `contract.Status` 字段。

## Decisions

### 以"停止前已记录的失败"为基准剔除，而不是"停止前是否 Failed"

停止前取内核状态 `before`；停止结束后，若 `before.Err != nil` 且返回的错误就是这个错误值或经 `Unwrap` 包裹了它，视为这次停止没有出错。永久停止（`retire`、动态子实例）还要求实例已到 Stopped，否则原样返回（期限先到时即 `ctx` 错误）。临时停止（持久 entry 撤门）在 `ctx` 未到期时等待实例落定，再对 `Wait` 的结果做同样剔除。

基准是已记录的错误而不是 Failed 状态：插件刚崩溃时内核已把失败记入 `n.err`，实例还在 Quiescing 中为这次失败清理，此时到达的停用若按状态判断，会把崩溃当成停止中出的错。这次清理由失败引起，不由停止引起；它的清理失败会并入同一个错误，并记在 `Outcome.CleanupCode` 与运行历史中。

按身份比较也挡住交错：`hostInstance.Update` 在交给内核前已释放子树预约，`Update` 与 `Disable` 可以交错：`Update` 清掉 `n.err` 并重启，随后被停止，新激活的启动失败或清理错误进入 `n.err`，它不是 `before.Err`，照常返回。

按错误值的身份比较，不用 `errors.Is`：`*contract.Error` 的 `Is` 按错误码匹配，而启动失败、崩溃与 guest 报告的错误大多同为 `plugin_failed`，`errors.Is` 会把交错中出现的新失败当成旧失败吞掉。比较时沿 `Unwrap() error` 与 `Unwrap() []error` 逐层查找，并先确认动态类型可比较，避免对不可比较的错误用 `==` 而 panic。包裹了旧失败的错误（失败本身的清理并入 `n.err` 时）照样剔除，这正是上面的崩溃窗口。

拒绝的备选：

- 以停止前 State 为 Failed 作为条件：漏掉正在为失败清理的窗口，且在 `Update` 交错时会吞掉新错误。
- 在内核 `Instance.Stop` 中不返回历史失败：内核规格与 `Host.Close`、加载器都依赖现有语义，改动面远大于问题本身。
- 在 `Disable`/`Remove` 调用处忽略 Failed 实例的错误：`Replace`、`Instance.Stop`、动态子实例有同样的问题，需要逐处重复；集中在 `hostInstance` 的停止路径更小。
- `Remove` 忽略 `disable` 的错误继续删记录：会在真实的停止失败（如期限到达）后删除仍在运行的 entry 的持久记录。

### 临时 `Stop` 返回本次停止的清理失败

撤门后先看 `ctx`：已到期则返回运行门停止的错误或 `ctx` 错误（同原实现，不重复拼接）；未到期则 `instance.Wait`，按上节剔除旧失败后与运行门停止错误合并返回。这样 `Instance.Stop` 与 `Disable` 对同一类错误给出一致结果。实例因清理失败进入内核 Failed 后，`Status` 仍按临时停止报 Stopped，`Outcome.CleanupCode` 记录清理失败，`Resume` 照旧经 `Restart` 恢复。

### 逐个调用方的决定

| 调用方 | 决定 |
| --- | --- |
| `Disable` → `disableEntries` → `retireOne(disable)` → `retire` | 采用新语义：停用并停下即返回 nil |
| `Remove` → `disable` | 不改代码：`disable` 不再返回历史失败，删除照常进行；真实停止错误仍阻止删除 |
| `Replace` → `retireOne(false)` | 采用新语义：旧版本已失败的 entry 不再让 `Replace` 报错，新版本的启动结果由随后的 `Wait` 报告 |
| `Instance.Stop`（持久，`halt`） | 采用新语义，并补上本次停止的清理失败 |
| `Instance.Stop`（动态子实例，`halt`；guest 经 `children` 的 stop） | 采用新语义 |
| `Host.Close` | 不改：规格要求关闭返回仍为 Failed 的实例的错误，且该错误不阻止 Store 释放与完成状态 |
| 加载器 `package:` 句柄 `loaded.Stop` | 不改：它适配内核句柄，加载器对进程内条目与 native 条目用同一语义，且停止错误只记日志，不阻止任何操作 |
| `Update` | 不涉及：更新 Failed 实例由内核清错并重启，不经停止路径 |

### 无原因的 Waiting 报 Starting

`Status` 先算出等待原因（运行门不算原因），再定状态：

1. 停用或临时停止、内核为 Waiting 或 Failed：Stopped（不变）。
2. 内核为 Waiting 且原因为空：Starting。
3. 内核为 Waiting 且有原因：Waiting，带原因。

`Applied` 按报出的状态推进：Starting 与 Quiescing 时不推进，与真正在启动的实例一致。原因为空的 Waiting 只出现在宿主或内核尚未处理到该实例的窗口（运行门异步启动、驱动未运行、条件评估在途），实例并不在等任何外部的东西，报 Starting 符合"启动正在进行"。没有必要新增原因种类。

拒绝的备选：

- 新增原因种类（如 `kind: "starting"`）：等于把"启动中"编码进 Waiting，嵌入方仍要特判；状态机已有 Starting。
- 让 `Enable` 等运行门就绪再返回：只覆盖运行门窗口，内核驱动与条件评估的窗口仍在，`Resume` 也要同样处理；还让 `Enable` 多一次等待。

## Risks / Trade-offs

- [依赖"停止已失败实例时内核 `n.err` 不变"这一内核实现事实] → 新测试直接覆盖 `Disable`、`Remove`、临时 `Stop` 与动态子实例停止已失败实例的结果；内核语义变化会让它们失败。
- [调用方的期限在停止完成前到达，随后清理失败；再次 Stop 或 Disable 时，这个失败已记录在 `n.err`，被当成停止前的失败剔除] → 宿主分不清它来自上一次被放弃等待的停止还是来自更早的失败；上一次调用已因期限返回错误，提示结果不确定，调用方按规格重新查询状态。
- [停用与崩溃几乎同时发生：内核尚未记录失败时到达的停止会把崩溃报告为停止中的错误] → 此时宿主确实无法区分；错误照常返回而不是被猜测性剔除，`lifecycle-smoke` 对这一情形只检查运行历史。正在为已记录失败清理的窗口没有专门测试，依据是内核源码（见 Context）。
- [运行门本身若启动失败，entry 会一直报 Starting 而不是 Waiting] → 运行门只在根作用域提供一个宿主保留的键，不经用户代码；此前同一情形报"无原因的 Waiting"，同样不可诊断，没有变差。
- [已依赖 `Disable` 返回历史失败的嵌入方会少得到一个错误] → 失败仍在 `Status.Outcome`（停止前可查）与 `Store().Runs()` 中，CHANGELOG 给出改法。
- [`Status` 从 Waiting 变为 Starting 的调用方判断] → `cordis run`、`plugintest` 已按 Starting 处理；对外以 CHANGELOG 公布。

## Migration Plan

Go API 行为变化，随次版本发布（`docs/stability.md`：修订版本只有修复，次版本可以有行为变化）。嵌入方删除对"Waiting 且无原因"的特判；需要在停用前知道 entry 是否失败的，先读 `Status` 或 `Store().Runs()`。不涉及存储或协议迁移。
