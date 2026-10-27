---
title: Go API 导航
description: 按包、使用者和工作流程查找 Cordis 的公开类型、方法与错误处理入口。
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - host.go
  - instance.go
  - management.go
  - diagnostics.go
  - config
  - plugintest
  - children.go
  - cascade.go
  - service.go
  - contract
  - sdk
  - packagehost
  - access
  - runtime
  - schema
  - wire
---

# Go API 导航

插件作者通常只需要 `contract`、`sdk`，生成服务还会使用 `schema`。宿主从根包 `cordis` 开始，按需配置 `packagehost`、`access` 和 Runtime。`internal/core`、`runtime/internal/process` 不是外部消费者可以导入的 API。

本页按用途说明入口和使用顺序；完整签名以当前检出的 `go doc` 为准。页尾代码基线对应本页核对的修订，[pkg.go.dev](https://pkg.go.dev/github.com/Cyenoch/cordis) 可能显示另一公开版本，不能据此假定新 API 已在旧版本中发布。

## 包一览

| 导入路径 | 使用者 | 内容 |
| --- | --- | --- |
| `github.com/Cyenoch/cordis` | 宿主、本地插件 | Host、类型化服务、级联计划 |
| `github.com/Cyenoch/cordis/contract` | 双方 | Scope、Definition、Entry、Identity、实例、事件、资源组、协议数据 |
| `github.com/Cyenoch/cordis/sdk` | 外部插件 | 标准输入输出会话、类型化服务、贡献、任务与子实例 |
| `github.com/Cyenoch/cordis/packagehost` | 宿主、打包工具 | 包格式、签名、信任、Store、恢复与运行记录 |
| `github.com/Cyenoch/cordis/access` | 宿主 | 授权、Broker、贡献、任务、namespace、脱敏诊断 |
| `github.com/Cyenoch/cordis/runtime/native` | 宿主 | 受信任原生进程运行时 |
| `github.com/Cyenoch/cordis/runtime/wasm` | 宿主 | 独立 WASI worker 运行时 |
| `github.com/Cyenoch/cordis/schema` | 双方、生成工具 | 受限数据 schema、代码生成、Optional / Nullable 与整数 codec |
| `github.com/Cyenoch/cordis/config` | 文件入口 | 严格 TOML/YAML/JSON、精确数值与位置映射 |
| `github.com/Cyenoch/cordis/plugintest` | 插件业务测试 | 真实 definition/codec/lifecycle 和显式业务 port |
| `github.com/Cyenoch/cordis/wire` | Runtime / 协议适配作者 | 有界 NDJSON Endpoint、Frame、Limits、请求关联 |

## Host：管理与调用

`cordis.Open(Options)` 要求 `Directory`，打开独占 Store；成功后必须处理 `Host.Close(ctx)` 的错误。它不会自动恢复之前的 entry。Options 中的主要配置如下：

| 字段 | 用法 |
| --- | --- |
| `Directory` | 宿主管理的持久包目录，不与其他 Host 同时打开 |
| `Trust` | 可信公钥、publisher 与精确包 ID 授权 |
| `Runtimes` | 按 `native` / `wasm` 显式启用；未注册的运行形式不可执行 |
| `FrameBytes` | 通用外部会话必需，4096–1048576；双向完整帧预算，无零值默认 |
| `Authorize` | 对 Manifest 的能力声明决定授予范围；nil grant 不授权 |
| `Grace` | 停止及控制调用的宽限期，默认 1 秒 |
| `DecodePackage` | 可信宿主元数据 decoder，不改变原始签名字节 |
| `SessionFactory` / `HandleGuest` | 宿主专有 ABI 适配；仍须保留准入、身份、取消与授权边界 |

包和实例的常用顺序：

```text
Install → Select → Enable → Instance.Wait → Call / Emit
                     ↓
                Update / Stop / Resume / Disable
```

| 入口 | 行为与注意点 |
| --- | --- |
| `Install(ctx, archive)` | 验证并安装包；不选择、不启动 |
| `Select(ctx, ref)` | 改变显式版本选择；不负责重启正在运行的实例 |
| `Enable(ctx, entry)` | 持久化外部 entry 并登记实例；返回不等于已经 active |
| `AddBuiltin(ctx, entry, definition)` | 注册进程内定义；不把函数或 builtin 配置写入 catalog |
| `RegisterDefinition(ctx, packageID, definition)` | 给该 builtin 包的动态子实例注册可用定义 |
| `Call(ctx, realm, requirement, input)` | 按 realm 和服务版本调用；不获得退役服务的清理绑定 |
| `Emit(ctx, realm, name, input, mode)` | 按 realm 范围分派事件 |
| `ConfigureRealm(name, parent, isolatedKeys)` | 配置服务继承与隔离，key 使用 `Service.Key()` |
| `Status(id)` | 查询 Host 持有实例的状态；未知 ID 返回错误 |
| `Resume(ctx, id)` | 恢复被 Stop 的实例，不重新注册一个同名 entry |
| `Disable(ctx, id)` | 停用并移除该树；与只停止实例不同 |
| `Replace(ctx, ref, options)` | 预检后替换所选包并重建目标实例；不回滚业务数据 |
| `Restore(ctx)` | 返回逐条 RestoreReport 与聚合错误；只补缺失登记，不隐式 Retry 或撤销临时 Stop |
| `Supervise(ctx, id, budget, delay)` | 有限失败重试；不重放业务请求 |
| `Close(ctx)` | 停止实例并关闭 Store；超时不代表不合作的 Go callback 已终止 |

`PlanCascade(ctx, ref, action)` 用于先查看影响；执行用 `ReplaceCascade` 或 `UninstallCascade`，必须传入真实 `ConfirmCascade`。`SelectPackage` 与 `RemovePackage` 是显式动作，不能按“ref 是否当前选中”猜测。确认回调不能重入 Host 管理操作。

`Store()`、`Access()`、`Broker()`、`Contributions()`、`Tasks()` 返回同一 Host 的基础设施。正常安装、启停和升级优先走 Host，不在其背后直接修改 Store 的启用意图或当前选择；低层 Store 不会替 Host 停止正在执行的代码。

完整代码见[宿主指南](host-guide.md)、[组合实操](composition-tutorial.md)和[上线维护](operations.md)。

## contract：定义、实例与 Scope

| 类型或入口 | 契约 |
| --- | --- |
| `Definition` | `ID`、`Requires`、`Provides`、可选 `Validate` 及 `Start`；必需服务就绪后才 Start |
| `Entry` | `ID`、`Package`、`Definition`、`Realm`、可选 `Parent` 和 JSON `Config`；持久意图不是运行进程 |
| `Identity` | Package、Entry、Activation、Digest；每次启动更换 activation |
| `Scope` | 当前身份/配置/取消、资源登记、服务调用与发布、事件订阅与发送 |
| `Service` / `Requirement` | name + major；provider minor 必须落在 requirement 的闭区间内 |
| `Instance` | `Status`、`Wait`、`Update`、`Stop`、`Retry` |
| `Status` | Enabled、TemporaryStopped、State、Desired、Applied、结构化 Waiting、Error 与 Diagnostics |
| `Child` / `CreateChild` | 在父初始化期间登记同包子定义，不等待子树启动 |
| `ChildInstance.Refresh` | 获取新远程状态；`Status` 仅返回快照 |

`Caller/Registrar/HostInvoker/Contributor` 是共用小接口；`Consumer` 绑定完整 Requirement 与方法契约身份。`contract.Go` 只在 Start 登记受管工作，`Own` 在登记资源失败时立即关闭。

Scope 的 `Context()` 表示 activation 生存期，handler 的 `ctx` 表示本次调用。调用其他服务时继续传入当前调用的 ctx；清理时使用 `Defer` 给出的 context，不能用已取消的业务 ctx 代替。

`Update(ctx, config)` 返回 `UpdateResult{Revision, Applied, Superseded}`。请求被接受不一定表示新 revision 已生效；取消等待不撤销已提交配置。先看错误，再结合 `Status.Desired/Applied` 判断收敛。`Stop` 保留实例，`Retry` 重试失败状态；`Disable` 是 Host 的树移除操作。

类型化入口按运行位置选择：

- 本地插件：`cordis.Provide(scope, Service[In, Out], handler)`、`cordis.Call(ctx, caller, service, input)`；后者请求 service 的精确 minor。
- 外部插件：`sdk.Provide(scope, service, handler)`、`sdk.Call[In, Out](ctx, scope, requirement, input)`。
- 事件：`contract.EventKey[T]`、`Subscribe`、`Publish`；显式选择 Sequential、Parallel 或 First。
- middleware：`contract.Chain(final, middlewares...)`；按参数顺序进入，逆序返回，每层 next 至多调用一次。
- 资源：`contract.NewGroup(owner)`、`Group.Defer`、`Group.Close`；零值可用，不得在使用后复制。
- 数据：`Encode`、`DecodeData` 处理严格 typed 数据；`Decode` 用于协议结构，不用宽松 `json.Unmarshal` 绕过边界。

## sdk：外部插件

程序优先用 `sdk.Main(definitions...)` 统一信号和退出码；需自行返回错误时用 `sdk.Run`。`Serve`、`ServeDefinitions` 和单定义自有 transport 的 `ServeTransport` 保留为低层入口。stdout 只能用于协议，日志写 stderr。

Start 收到的 `*sdk.Client` 实现 Scope 和 ChildScope，另提供：

| 方法 | 用途 |
| --- | --- |
| `InvokeHost(ctx, capability, input)` | 请求宿主能力；不是直接访问操作系统 |
| `Contribute(value)` | 初始化期间登记声明式贡献，受宿主 slot validator 校验 |
| `BeginTask(ctx, id)` | 建立 activation 任务并生成唯一 token |
| `CreateChild(ctx, child)` | 返回远程子实例 handle，父发布后才可能启动 |

`BeginTask` 返回共用 `contract.Task`，提供 `Context`、`Finish` 和所有终态的显式 `Release`；没有宿主 `Transfer` 权限。任务、贡献和 namespace 接线见[访问实操](access-tutorial.md)。

## packagehost：包与持久状态

作者入口 `LoadAuthor` 读取唯一 TOML/YAML/JSON 源，`Author.Generate` 生成类型和元数据；`Build` 生成、构建并记录实际 SDK 与产物摘要，`PrepareBuild/WriteBuildDescriptor` 供显式外部流水线。打包入口为 `Pack`、`Sign`、`Inspect`；需要已有宿主 Manifest 时使用 `WithDecoder` 版本。Inspect 只校验格式和完整性，不授信。`Discover` 对显式来源集合返回候选及逐项错误，不下载、不自动求解版本、不执行。

`Open`、`OpenWithLimits`、`OpenWithOptions` 创建 Store。主要方法按用途分组：

| 用途 | 方法 |
| --- | --- |
| 安装及查询 | `Install`、显式开发 `ImportDirectory`、`Get`、`Installed`、`Selected` |
| 版本与依赖 | `Preflight`、`CheckSelect`、`Select`、`ReverseDependencies` |
| 执行副本 | `Snapshot`；执行完成后处理 `Snapshot.Close`，不能用 Get 的 Directory 启动 |
| 启用意图 | `Entries`、`SaveEntry`、`DeleteEntry`、`RegisterParent`、`UnregisterParent` |
| 数据版本 | `ConfirmDataVersion`、`DataDirectory` |
| 信任与发现 | `UpdateTrust`、`Discover`、`Limits` |
| 卸载 | `CheckUninstall`、`Uninstall`、`PendingRemovals`、`RetryRemovals` |
| 运行记录 | `BeginRun`、`EndRun`、`Runs` |
| 结束 | `Close` 释放跨进程独占锁 |

低层 `ChangeOptions.InUse/Enabled` 是宿主提供的完整事实，不是要求忽略冲突的开关。数据迁移确认一旦提交，后续选择失败也不能恢复旧数据假象。`CommitError` 表示已过提交点；恢复和重试规则见[包参考](packages.md)及[恢复指南](recovery.md)。

## access：宿主策略

- `Policy.Activate/Grant/Check/Revoke/RevokeCapability` 管理当前身份与授权，`Calls` 观察取消但尚未结束的 handler；真实 Host 已接管激活/撤销，不接受 guest 自报身份。
- `Broker.Register/Call` 组合声明范围、grant 范围和输入检查，再调用宿主 handler。
- `ContributionStore.Register/Stage/Snapshot`、`Batch.Commit/Abort` 管理整批发布；连接 Host 时服从 activation gate。
- `TaskManager.Create/Finish/Release/Snapshot` 与可信 `Task.Start/Transfer/Finish/Release` 管理取消权和有界结果，不持久化业务事务。
- `NewNamespaceStore` 明确使用内存；`OpenNamespaceStore` 使用持久日志。`Configure/Read/Write/Delete/Migrate/DeleteNamespace/Compact` 由宿主调用。
- `access.Register[I,O]` 复用同一 broker；`RegisterNamespace` 绑定可信 package namespace，SDK 的 `NewNamespaceClient` 保留确定 outcome 和 submission-unknown，不自动重试。
- `Sink.Emit/PublicError` 将宿主诊断与 guest 可见错误分开；默认不复制原始错误、载荷或路径。

## Runtime、schema 与 wire

`native.New(Options)` 和 `wasm.New(absoluteWorkerPath, Options)` 都实现 `contract.Runtime.Start(ctx, Launch)`，返回 `contract.Session`。Start ctx 只约束启动；使用 Options.Lifetime 管理会话父生存期。运行时限制、环境白名单和缓存见[运行时参考](runtimes.md)。自定义 Runtime 必须保持会话取消、停止与回收契约。

`schema.Parse/Generate/GenerateMany` 处理单源配置、服务和 definition 声明；`Specs/Fingerprint` 提供确定描述，`Export` 输出编辑器用标准 JSON Schema 2020-12；`Compile` 返回可并发复用的不可变 schema，`Validate` 校验数据。`FieldError` 提供 JSON Pointer、Keyword 与 Message。可选值用 `Optional` / `Some`，可空值用 `Nullable` / `NonNull`，64 位整数用 `Int64` / `Uint64` 字符串 codec。见[生成实操](generated-service.md)。

`wire.New(Options)` 返回 Endpoint，提供 `Call`、`Done`、`Err`、`Close`。Host 必须显式提供 `Options.FrameBytes`，Guest 从必需 initialize 绑定；`DefaultLimits` 仅含 64 个在途、默认 30 秒、取消宽限 1 秒，不含帧默认值。Endpoint 不负责领域授权。已有宿主协议通过 SessionFactory / PluginSession 适配，不要求外部插件作者手写 Frame；详见[协议](protocol.md)。

## 错误与本地查询

用 `errors.As(err, &target)` 检查 `*contract.Error`、`*schema.FieldError`、`*packagehost.CommitError`、`*access.CommittedError` 和 `*access.RecoveryError`；不要解析错误字符串。`contract.Error` 的 canceled / timeout 支持 `errors.Is` 匹配标准 context 错误。错误码对照见[排障](troubleshooting.md)。

在目标版本检出中查询：

```sh
go doc github.com/Cyenoch/cordis.Host
go doc github.com/Cyenoch/cordis/contract.Scope
go doc github.com/Cyenoch/cordis/sdk.Client
go doc github.com/Cyenoch/cordis/packagehost.Store
go doc github.com/Cyenoch/cordis/access.NamespaceStore
go doc github.com/Cyenoch/cordis/schema.FieldError
```

查询你自己的依赖版本用 `go list -m github.com/Cyenoch/cordis`。不要把本地 replace、另一个分支的源码或站点最新文档当作当前依赖包含该符号的证据。
