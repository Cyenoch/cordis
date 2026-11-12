## Why

文档说应用用 `host.Caller(entryID)` 取得某个 entry 的解析视图"再交给生成的客户端"，但 `Caller` 只有无类型的 `Call`，生成的 `NewXClient` 要的是 `contract.Endpoint`，流式方法在 entry 视图里根本调不了。两个隔离的 entry 提供同名服务时，应用无法用类型化客户端分别调用它们，根作用域又看不到它们。宿主提供给 native 插件的服务也看不到请求来自哪个激活，只能让插件在每个请求里自报身份。另外，`docs/protocol.md` 与 `docs/composition.md` 声称 `parent` 识别调用环并以 `reentrant_call` 拒绝，实现并没有这项检测。

## What Changes

- 新增 `hosting.Caller.Endpoint(required)`：按 entry 的视图解析服务并校验版本范围，返回当前 provider 的 `contract.Endpoint`，可以交给生成的 `NewXClient`，一元与流式方法都能调用。`Caller.Call` 改为经由它。
- `Caller` 绑定取得它时的那次登记。entry 被停用、移除或替换后，`Endpoint` 与 `Call` 以 `not_ready` 失败，不再按根空间解析未隔离的服务；同一登记的恢复重启不影响视图。
- 新增 `hosting.Requester(ctx)`：Host 代 native 激活处理的每个请求（调用、流、事件、链、子实例操作）在 ctx 上带着发起请求的激活身份（`contract.Identity`）。应用和进程内插件发起的调用报告没有请求方。身份只是信息，不是授权。
- 更正 `docs/protocol.md` 与 `docs/composition.md`：`parent` 把请求关联到正在处理的请求，被关联的请求继承它的取消与期限；回调并发处理，不当作环拒绝。不新增环检测。
- 线协议、包格式与生成代码不变。

## 非目标

- 不做授权，不校验插件是否"有权"调用某个服务；需要授权的应用基于身份自行判断。
- 不把请求方身份传给另一个插件：线协议没有这一字段，本次也不加。
- `host.Caller` 仍只覆盖持久 entry，不覆盖动态子实例。
- 不为进程内插件伪造 `contract.Identity`：进程内插件没有包、entry 与摘要。

## 兼容边界

- 两个新增导出 API 属于 v0 次版本允许的 Go API 变化，线协议与包格式不变（见 `docs/stability.md`）。
- 对已停用、移除或替换的 entry，旧 `Caller` 由"仍能解析未隔离服务"改为明确失败。这是对既有规格"旧视图明确失败"的更正，不保留旧行为。

## 来源区分

- 源码事实（`7cb8d3e`）：`Caller` 只保存 entry 的作用域，`Call` 每次解析并校验版本；Host 处理 native 请求的 `run.handle`/`run.stream` 持有激活身份，但不交给处理者；`wire` 只用 `parent` 做初始化准入和 ctx 继承，非编码错误一律成为 `plugin_failed`。
- 历史：初版设计（`2026-10-28-build-go-plugin-framework`）有串行槽位和 `reentrant_call` 环检测；`2026-11-11-rebase-native-on-kernel` 改为处理函数并发执行，去掉了串行槽位与调用环检测，文档没有随之更新。
- 框架决定：见 `design.md`。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-composition`: entry 解析视图提供可交给生成客户端的端点，旧视图明确失败。
- `framework-execution`: 宿主服务可见发起请求的激活身份；请求关联与回调语义。

## Impact

- `hosting`：`Caller.Endpoint`、绑定登记的 `Caller`、`Requester`，bridge 标记请求方。
- 文档：`docs/host-guide.md`、`docs/composition.md`、`docs/kernel.md`、`docs/protocol.md`、`CHANGELOG.md`。
