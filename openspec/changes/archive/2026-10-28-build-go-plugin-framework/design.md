# Design

> 历史记录：外部宿主专名与路径已匿名化，原始内容可从 Git 查看。本记录不定义当前能力。

## Context

动机与兼容边界见 [proposal.md](proposal.md)，行为契约见 `specs/` 的五项 capability。本文件同时记录既有设计与新增待实现决策，不以文档存在表示功能已落地；实际执行记录、平台限制和任务状态见 [tasks.md](tasks.md)。第 15 节为本轮开发体验规划。

现有事实：

- 框架最初暂驻 外部宿主 的嵌套 module。2026-10-27 用户确认迁至独立 Cordis 仓库 `github.com/Cyenoch/cordis` 并公开发布，MIT 不变；不为本主题另建镜像设计文档。
- 外部宿主 的插件契约由相邻 `[外部宿主插件规格]` change 拥有，要求 WASM 独立 worker、NDJSON、activation 撤销、宿主能力代理与声明式 UI。通用框架不能反向改写这些契约。
- 参考源码固定于 `third_party/cordis` 提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88`，不是运行时依赖。以下源码事实均指该提交：

| 源码事实 | 证据：仓库相对路径 |
|---|---|
| core 接收函数或 apply 定义，同一定义可拥有多个实例 | `third_party/cordis/packages/core/src/registry.ts:63-99,144-150,193-213` |
| 注入 key 都是必需依赖，不存在语言层 optional 注入；缺失时等待 | `third_party/cordis/packages/core/src/registry.ts:43-61`；`third_party/cordis/packages/core/src/fiber.ts:381-405` |
| provider 退役等待 committed consumers 清理 | `third_party/cordis/packages/core/src/reflect.ts:202-237`；`third_party/cordis/packages/core/src/fiber.ts:426-483` |
| effect 内逆序串行；fiber 的顶层 effects 并发清理，不是全局 LIFO | `third_party/cordis/packages/core/src/fiber.ts:285-305,457-475` |
| loader 才负责 import，加载模块与启动实例分离 | `third_party/cordis/packages/loader/src/config/entry.ts:330-371` |
| emit、parallel、serial、bail、waterfall 的同步/等待/短路语义不同 | `third_party/cordis/packages/core/src/events.ts:6-8,95-140` |

已观察的隔离实验：固定 Cordis 消费者等待 provider，provider 下线先清理消费者；Go 1.27.3 + wazero v1.12.0 在 darwin/arm64 加载独立 Go WASI reactor，双实例独立、关闭后调用失败、无限循环受 deadline 终止。实验文件已删除。该证据不覆盖本框架的包格式、协议、SDK、worker、安全策略或其他平台。Go reactor 能力参见 [Go 官方说明](https://go.dev/blog/wasmexport)；本设计首版选择下文的 WASI command 会话，不能把 reactor 实验当作该会话已通过验证。

## Goals / Non-Goals

**Goals:**

- 核心 Interface 足够小，隐藏依赖退役、竞态和资源归属复杂度；进程内使用静态类型，跨运行时使用显式数据契约。
- 外部包与两个真实 Runtime Adapter 都是交付要求；先完成 core 不是缩减交付范围的理由。
- 控制面不被插件业务处理器阻塞；停止、取消和诊断可以独立推进。
- 框架与宿主在包、权限、UI、事务和协议扩展处形成明确 seam。

**Non-Goals:**

- 不复刻 JavaScript Proxy、原型链、装饰器、truthiness、Node module cache 或 HMR 内部实现。
- 不把进程内受信任 Go 回调描述为可强制终止的沙箱，也不把原生进程签名描述为权限隔离。
- 不提供透明 Go interface RPC、网络远程插件、市场、自动下载依赖、分布式事件或自动业务回滚。

## Decisions

### 1. 独立仓库与单一规划源

2026-10-27 用户确认将框架整体迁至独立仓库 `github.com/Cyenoch/cordis`，本地开发目录为 外部宿主 相邻的 `../cordis`，采用 MIT 并公开发布。此决定取代最初暂驻嵌套 module、暂不发布的决定。

本 change 与参考源码随代码整体迁移，是唯一规划源。通用代码、SDK、工具、作者文档和测试无需检出 外部宿主；宿主专属 Adapter、原始 ABI fixture 和 smoke 由 外部宿主 的 `pluginapi/adapter/`、`internal/plugintest/` 和 `conformance/` 拥有。框架不导入 外部宿主 module，也不存放其领域 codec 或快照。

Integrator 独占 `go.mod`、公开契约、schema 与 OpenSpec。共享 Go/OpenSpec skills 复制并保留来源与许可证，数据库、MyGo 和游戏材料留在宿主。外部宿主 消费固定公开版本；本地联调可显式使用临时 workspace，不将相邻目录写成发布依赖。独立作者示例保留指向当前 checkout 的局部 replace，以验证正在修改的 SDK；这不是库发布依赖，根 module 没有 replace。

项目和工具名称更改不改变既有 `cordis-go.*` schema 标识及 `cordis-go-package/v1` 签名域。本轮按用户最新决定直接修订未稳定发布的契约，不为旧 v1 字段语义保留兼容路径，具体范围见第 15 节；签名算法及输入布局无需改变。

否决：嵌入 `[外部宿主内部目录]/` 后再抽取，或用多个 module 共享 外部宿主 内部类型。两者会把领域依赖和发布节奏带入框架。

### 2. Module ownership 与依赖方向

以下路径均相对于 Cordis 仓库根：

| 写集/Module | 拥有的 Implementation | 允许依赖 |
|---|---|---|
| Integrator：根公开契约、`contract/`、`wire/`、`schema/` | 身份、版本、契约描述、错误、消息类型和 schema | Go 标准库、审查后的基础依赖 |
| `internal/core/` | 实例协调、作用域、服务绑定、调度、effect 与更新 | 仅 `contract/` 等独立契约包，不导入根 Facade、Runtime 或包存储 |
| `packagehost/` | ZIP、校验、安装日志、包目录、entry 与版本选择 | 公开契约及 core 宿主入口，不执行插件代码 |
| `runtime/wasm/`、`cmd/cordis-worker/` | WASI 会话、wazero、资源限制和 worker 生命周期 | `wire/`、公开 Runtime seam |
| `runtime/native/` | 子进程、stdio、退出和受管进程树 | `wire/`、公开 Runtime seam |
| `access/` | grant/lease、任务所有权、贡献注册与脱敏入口 | 公开契约，不导入 UI/游戏 |
| `sdk/`、`cmd/cordis/` | 作者 Interface、契约生成、pack/sign/validate/inspect/run | 公开契约；工具可组合 Package Host 与 Runtime |
| `conformance/`、`examples/` | 实际宿主、独立作者 module、契约快照与故障包 | 仅公开 Interface；不导入框架 internal |

根宿主 Facade 组合 core、Package Host、access 和 Runtime；共享类型位于 `contract/` 等叶子契约包，core 和 Adapter 均不反向导入根 Facade，避免循环依赖。SDK 不依赖宿主实现，WASM SDK 不携带 wazero；生成客户端和服务端共享 schema，不共享实现。

否决：一个带几十个方法的通用 Context、反射式自动 DI、全局 init 注册以及每个内部类一个接口。真实变化只有两个 Runtime、两种包来源及宿主策略，只有这些 seam 提供可替换 Adapter。

### 3. 身份、状态与控制协调

身份分为 PackageRef（ID/version/digest）、DefinitionID（包内稳定名称）、EntryID（持久配置实例）、ActivationID（每次实际启动产生的随机身份）。SDK 动态注册和每条会话操作都绑定 ActivationID；重新启用、配置重启、provider 重绑定或 worker 重启都生成新身份，不复用旧授权。

安装状态和运行状态分开：包存储记录安装、选定版本与信任；entry 保存 enabled 和期望配置；activation 具有 `WAITING -> STARTING -> ACTIVE -> QUIESCING -> STOPPED`，初始化/协议错误转 `FAILED`。desired/applied revision 是当前 Engine 的协调状态，重开按持久配置创建新激活。`FAILED` 不因依赖抖动自动重试；被取代配置有单独结果，不等同失败。

协调器串行提交图与状态变更，用户回调与 I/O 在协调锁之外执行。每次完成携带 activation 和配置 revision，仅当前目标可提交。关闭先拒绝普通业务准入和新注册，通知普通在途调用及受管工作取消，再退役服务并等待消费者清理；保留独立的 cleanup 准入及清理 context，最后释放 provider 资源并关闭 Runtime。取消普通工作不能先取消清理所需的 context，也不能等消费者结束后才通知。一个 entry 不允许新旧 activations 同时对外 ACTIVE。

注册被暂存并整体提交为一个 activation snapshot；宿主读 contribution 和服务时共享该提交点。外部 I/O 和已发事件不属于此事务。初始化期间不得依赖自己尚未发布的服务形成循环；发现动态等待环则诊断参与者，阻止循环激活，而不是永久等待。

### 4. Go Interface 与组合语义

使用独立的 `Scope` 表达资源与命名空间，`context.Context` 仅用于调用取消和 deadline，不存服务。泛型键/顶层泛型函数用于进程内类型化注册与解析；泛型方法不是设计前提。宿主显式安装 Definition，不依赖包 init 的隐式执行顺序。

服务键包含契约名及 major，消费者声明支持 minor；同一 realm 的同一键只能有一个已提交 provider，重复注册报错。消费者启动时取得 binding snapshot，provider 退役后不接纳新 binding，但 cleanup 仍可使用已持有绑定。只有通过该 binding 发起的清理调用可在 QUIESCING 阶段准入；它不是可用于新增业务或跨 scope 的额外通道。

必需依赖缺失时等待；可选功能通过依赖子 Scope 表达，不额外发明 optional 注入分支。隔离依服务键显式配置，局部配置 overlay 不修改共享服务对象。父关闭向子传播，子失去依赖不关闭父。

Scope.Defer 将清理归属当前激活；公开 `contract.Group` / `NewGroup` 提供幂等共享关闭，组内逆序串行，聚合错误和 panic 后继续其余清理。跨插件按服务和父子所有权依赖退役，不承诺跨组全局 LIFO。取消等待不取消共享释放；未完成的进程内回调保持 QUIESCING，依赖不能被虚假释放。

配置更新先校验并记录 desired revision，停止旧激活后按新配置启动；不采用 Cordis 的 config commit 后 veto restart 行为，也不提供原地热修改。applied revision 只在成功发布时前进。更新被后续版本覆盖时返回 superseded；不存在把所有等待者都记为成功的行为。

分发分为顺序通知、受限并行通知、显式 handled 结果和 middleware。顺序通知首错返回；并行等待全部、聚合错误；handled 可携带 0、false 或空字符串；middleware 的 next 每次调用只可执行一次。与 TS 版五种函数不是逐名兼容。

每个实例的受管业务回调默认串行；并行分发只允许独立实例或契约显式允许的并发。SDK 不声称能管住用户自行启动的裸 goroutine。控制消息、取消和会话读取不占业务回调队列。调用链及宿主等待图同时记录同步等待和串行执行占用；新增等待边构成环时返回 `reentrant_call`，不自动重入。用户可改为完成当前调用后投递事件。

### 5. 首版包格式与发现

使用一个 ZIP 格式（`.zip`），不引入多种压缩 Adapter。正式包包含 `plugin.json`、运行产物、数据契约/贡献文档、`integrity.json` 和可选 `signature.json`。开发目录由显式路径导入，先读取并复制为不可变暂存快照再验证；不原地执行可变源文件。开发 reload 是重新打包和显式替换，不是 Node HMR。

通用 Manifest 的 schema 标识为 `cordis-go.package/v1`：

- ID、SemVer、publisher、包内定义清单和默认定义；每个定义声明启动所需服务的契约名、major 和兼容 minor，以及可能提供的契约。宿主在执行初始化前据此等待和检查循环；动态可选功能注册为依赖子 Scope，不通过先执行初始化来探测启动依赖。
- 会话协议 `{major,minMinor,maxMinor,requiredFeatures}`，协商最高共同 minor，注册确认 minor/features。
- 必需/可选包依赖使用互斥的 `[Min,Max)` 区间或严格比较器交集 `Range`（`>=`、`>`、`<=`、`<`、`=`、`^`、`~`）；拒绝 OR、通配符和缩写，不做网络解析、自动版本求解或模糊 latest。
- `runtime.kind` 为 `wasm` 或 `native`；WASM entry 唯一，原生产物按 GOOS/GOARCH 显式列出且仅选择精确目标。
- 请求能力、贡献文件引用、definition 级配置 schema、数据版本和保留偏好；宿主定义的值放在受 schema 验证的扩展文档，不混入通用字段。

服务/requirement 是逻辑标识，上限 512；贡献 ID 上限 200，允许宿主合法的连续点。二者不参与文件路径拼接。包/publisher/definition/slot 名保留原 200 上限和连续点拒绝，归档文件继续使用独立路径规范化；避免把 外部宿主 精确服务键（最长约 303 字符）错误套入包路径限制。

JSON 严格解码、重复 key 与未知通用字段拒绝。当前契约在稳定发布前直接同步修订，不增加旧字段别名、探测式降级或并行解码器；稳定发布后的版本演进不属于本轮。未知宿主扩展不会自动信任。

包内路径为规范化相对 POSIX 路径，拒绝绝对/父路径、反斜杠、控制字符、非 NFC、驱动器/设备/UNC、Windows 保留名、尾点空格、链接、加密 ZIP、重复/大小写折叠冲突。安装限制由宿主给定并报告，默认 archive 64 MiB、单文件 64 MiB、总展开 256 MiB、10,000 条目；读取过程中按实际字节计数，不信任 ZIP 头。

完整性表列出除 integrity/signature 外的每个常规文件，按 UTF-8 路径字节排序并覆盖路径、文件大小和 SHA-256；不允许未列文件或指向缺失文件。签名输入固定为 ASCII `cordis-go-package/v1` 加一个 NUL、uint32 大端条目数，再逐条连接 uint32 大端路径字节数、UTF-8 路径、uint64 大端文件大小及 32 字节 SHA-256；不使用非确定性 JSON 文本，也不采信 ZIP mode。包 digest 是该输入的 SHA-256，Ed25519 签名覆盖该输入；publisher 在被摘要覆盖的 Manifest 中。`signature.json` 仅保存 keyId 和签名，key 由宿主 TrustPolicy 解析并检查与 publisher 的绑定及吊销状态。

默认 TrustPolicy 拒绝未授权签名或缺失签名；同时检查 keyId 与 publisher 的绑定，以及该 publisher 对 plugin ID 的发布授权。已安装 ID 的 publisher 更换须由宿主显式批准，不因更高版本或另一个有效签名自动接续原身份/数据；密钥轮换仍属于获准 publisher。dev 许可限定 digest，不能全局静默允许。签名只证明来源一致，不证明代码质量。包来源使用 archive 与 ImportDirectory；元数据扩展 seam 为 `MetadataDecoder(ctx, rawManifest, fs.FS)`，在受限私有快照中投影 `contract.Manifest`，`HostMetadata` 保留原始签名字节。Pack/Sign/Inspect/Open 与 Facade 使用同一 decoder，不绕过完整性、路径、信任和 activation 检查。

发现只枚举候选，不自动启用；同 ID/version 不同 digest 拒绝。已安装版本可并存，选择记录唯一且显式。重复候选报来源，不能按文件系统顺序覆盖。包静态依赖预检与运行时 service 图分离。

### 6. 受管安装、更新与恢复

受管内容按 digest 保存；解压和校验在同文件系统私有 staging 完成。durable catalog 的一个原子根记录分别包含已安装 PackageRef 集合、每个 ID 的 selected 引用以及 entry 配置；安装只增加已安装集合，不改变现有 selected 或启动插件。显式版本切换才修改 selected。事务记录、同步文件和原子替换 catalog 根组成提交步骤，catalog 根是该事务唯一提交点；恢复和提交后取消均依据对应事务提交状态，而非只看 selected。安装操作串行提交，跨进程使用存储锁，不从半写目录推断成功。

加载从受管内容生成本次执行的私有快照并重新核验摘要与信任，再将字节提供给 WASM 或按受控路径启动原生产物。抵御被插件修改下一次加载内容；不声称能抵御拥有同用户完全权限的敌对原生进程或系统管理员并发篡改，原生 Runtime 本身就是受信任模式。

更新先验证新包及全部已启用反向依赖的版本范围；不兼容时拒绝切换并列出依赖方，只有宿主明确授权后才先级联停用。然后 quiesce 旧激活、切换并初始化。失败时保留两份代码；若数据版本未改变，返回可显式恢复旧选择的结果，不无条件自动重试。若宿主已确认数据迁移提交，不自动降级；返回宿主恢复所需元数据。数据迁移属于宿主，首版框架不在失败后猜测运行旧迁移。

卸载仅移除未被运行和必需依赖使用的代码，保留数据为默认；宿主确认删除后才调用数据删除策略。Windows 上先关闭受管进程/句柄，移至待删除区域；仍被外部程序占用则返回 pending removal，不能假报代码已移除。宿主重启后重试已记录清理，不影响选定的其他包。

### 7. 会话与远端服务

首版通用协议为 `cordis-go.session/v1`，UTF-8 NDJSON，每行一个帧，stdout 只承载协议，stderr 只承载有界日志。默认帧 1 MiB、深度 32、数组 10,000、最多 64 个在途调用；宿主可收紧。必须在实际解码前限制读取长度。

帧分为 request、response、cancel；包含 schema、activation、request ID、method、payload/error、剩余 deadline 和调用关联。request/response 必须匹配会话、方向、方法和请求 ID；request ID 在方向内不复用并包含方向前缀，避免双向同时生成的碰撞。已取消请求保留有界终态记录，其迟到响应只记诊断；重复有效响应、未知关联或协议破坏关闭对应会话。旧 activation 永不提交状态，旧连接直接关闭。cancel 仅针对当前已知请求，重复取消幂等。

控制方法为 initialize、health、shutdown；业务原语为 call、event、register 和 host.invoke。初始化协商协议与必需特性，不接受来宾自报身份替换宿主身份。当前 SDK 与宿主限额尚未通过握手统一，此项由 `negotiate-session-frame-budgets` 修订，不把原设计意图写成已实现。错误使用稳定 code（incompatible、invalid_message、not_ready、denied、timeout、canceled、reentrant_call、plugin_failed、internal）与脱敏 details，handler 的业务错误由契约定义。

注册使用契约 ID、major/minor、方法和可序列化 schema。Integrator 拥有受限 JSON Schema 数据模型及生成器输入：对象、数组、字符串、布尔、有限数值、显式 nullable 和标记联合；字节用 base64，64 位整数用十进制字符串，防止 JSON 数值丢失。拒绝任意 interface、函数、指针和循环类型。生成 Go 客户端/服务端及 schema 校验代码；进程内同契约可直调，跨运行时走代理。

provider/consumer 的租约和 binding 身份留在宿主，插件只持不透明引用；外部插件互调也经宿主路由和授权。关闭或取消后迟到响应只记诊断，不提交结果。取消不撤回外部副作用，也不自动重试调用。

调用链由宿主维护；插件只携带当前会话内可验证的 parent request 引用，不能自报 activation 链以绕过检测、扩大 deadline 或冒充其他实例。宿主将子调用绑定到已准入父调用，限制链深和剩余时间。路由在排队前同时检查调用链与等待图；包括独立根调用分别占用 A/B 再同步调用对方的跨链环，形成环则拒绝新增等待边。等待结束、取消、失败和会话退出均删除对应边。控制读取与业务调度分离，不能在生命周期锁内等待 RPC。通知不承诺持久投递；跨激活不自动重放。

否决：gRPC + 第二套 NDJSON 并存、透明 reflection RPC、复用 Go 内存布局为 ABI。首版选择一种有界编码使 WASM、native 和宿主适配复用校验与工具。

### 8. Runtime Adapter

**WASM：** 采用 wazero v1.12.0（Apache-2.0，许可证随 module 留存），WASI preview 1 **command** 模块。Go 作者用 `GOOS=wasip1 GOARCH=wasm go build`，SDK main 运行受管会话循环；默认通过随宿主交付的独立 `cordis-worker` 运行，每个 activation 独立 worker 和 module。

worker 不继承敏感环境、不 preopen 目录、不提供任意 socket/进程 import；只有 clock/random 和会话 stdio 等明确 WASI 配置。runtime 开启 `WithCloseOnContextDone(true)`、内存上限和编译/启动/调用 deadline。关闭同时关闭会话管道以解除 host I/O 阻塞，超时强制终止 worker；CPU loop 的取消不等于任意 Go host function 自动可中断。host handler 自身必须使用 context；不可取消的宿主业务由宿主管理，不能伪报取消完成。

WASI SDK 使用标准 Go goroutine，独立读取 response/cancel/控制消息；业务回调串行，等待环由宿主拒绝，不开放手动 Cooperative/Pump API。worker 必须保留可 poll 的 `*os.File` stdin，不能通过普通 Reader wrapper 丢失非阻塞能力。Windows 仅对原 poll 的 WASI EIO 加实际 BROKEN_PIPE 合成 stdin HANGUP，预先拒绝溢出/缓冲区交叠。成功 shutdown 完整写出确认后退出；宿主在锁内发布匹配结果，预期进程退出先有界读取自然 EOF。反应器不是第二种 ABI。

**Native：** 直接启动精确平台产物，不经 shell；包内 entry 必须是已验证常规文件。私有执行副本在 Windows 固定为 `plugin.exe`，不依赖包内扩展名。独立 stdio 会话，环境白名单、私有 cwd、stderr 限额、退出检测及有界 shutdown。Unix 使用受管进程组，Windows 使用 Job Object；同步 pipe Close 独立推进，不阻塞终止/kill/reap，Job drain 后对映像延迟释放仅窄重试删除。错误保留；脱离管理范围或自行 daemonize 不属于进程管理承诺，宿主策略可完全禁用 native。

每个 activation 单独进程，不把整个包的所有实例塞入同一进程。宿主显式提供有限重启预算；每次重启生成新 activation，不自动重试业务请求。后台任务仅 SDK 管理的任务可被等待，原生第三方裸 goroutine 不在生命周期承诺内。

**Builtin：** 显式编译注册，用于受信任模块，沿用 Scope 与宿主领域 ports；不是外部包的兜底路径。无法强制杀死不合作的 Go goroutine，因此报告 QUIESCING/blocked，不宣称等同外部 Runtime 的故障隔离。

### 9. Access、贡献与任务

通用 access Module 只拥有 owner、grant/lease 身份、撤销 epoch、调用准入与审计入口；宿主实现范围匹配及真实 I/O。每次准入读取当前 activation 与授权状态，撤销和准入共用一个线性化点。先准入的操作收到取消；撤销后不再准入，不能声称已取消所有系统调用。lease 不能由来宾自造。

贡献存储按 `(slot,id,activation)` 建立暂存批次，宿主 validator 全量接受才与激活一同发布；revoke 移除该 owner 的 snapshot，不扫其他实例。UI schema、渲染和 accessibility 归宿主，框架仅传贡献数据与状态。

任务默认 activation-owned，状态为 queued/running/completed/failed/canceled/interrupted，终态只可提交一次。显式 transfer 将所有权交给宿主，移交后不再被插件 cleanup 杀掉。checkpoint 数据、任务恢复与 journal 归宿主；框架提供回调和所有权记录，不另建持久任务引擎。

存储由宿主代理提供 namespace、quota 和 data version；SDK 不给 guest 任意路径。停用不删除持久数据。诊断使用标准结构化日志及稳定错误 code，默认只记录身份、阶段和经过脱敏的摘要；不默认回显配置、消息或插件日志里的敏感字符串。

### 10. 外部宿主 seam 与验收

不把通用 schema 更名成 `[外部宿主 schema]`，也不要求 外部宿主 改成通用协议。宿主仓库 `pluginapi/adapter/` 拥有契约快照、Manifest/会话 Adapter，`internal/plugintest/` 拥有领域无副作用的 fixture host；它们从宿主 `[外部宿主插件规格]` 的三个 delta spec 与 design 记录来源路径和摘要，变化须显式审查。Cordis 独立验收不读取宿主文件。

Adapter 保持原始 12 个方法，映射 initialize/health/shutdown、command/query/event、provider/service、capability/job 及维护会话 data.migrate。外部宿主 事件与 job 保持请求/响应；uint64 snapshot/计数保持规范十进制字符串，provider/service 保留分页与完整 envelope。Manifest.services/provider.requires 使用 package/provider/contract 精确依赖；同包命令与多个同 contract provider 可共存，不通过 builtin gateway 补依赖。grants 是宿主解析的范围投影，Broker 每次重新检查实际策略。维护会话不发布普通贡献、不授普通能力，只返回纯数据计划；真正 namespace 事务与恢复归 App。WASM 仍由独立 worker 执行，不执行双重 envelope。

验收使用真实 Go WASM 插件和 worker，覆盖授权裁剪查询、秘密不可见、声明式贡献批量拒绝/撤销、迟到 activation、超时、进程崩溃，以及已接管任务不因插件退出被杀。框架原生 Runtime 的通用验收不代表 外部宿主 已允许 native Manifest。

框架任务不实现 MyGo 页面或真实游戏部署，不复制宿主三份规格为新产品规范。fixture host 通过仅证明协议 seam 和通用机制；UI 可访问性和真实部署恢复仍由对应宿主 change 验证。

Stardew MVP 是本框架的实际消费出口，必须使用公开服务/依赖、包、WASM 与资源所有权机制，不以静态注册或 conformance 示例替代。框架可先按固定宿主契约夹具实现，不等待 MyGo/游戏源码；真实 Adapter 与业务包由平台/MVP change 交付，避免框架与宿主互相等待实现。宿主契约修订时显式更新快照与覆盖报告，不反向改变通用机制或冻结旧快照来掩盖差异。

### 11. 验证与发布

先用公开 Interface 进行行为验证，再执行两个外部插件 module 的真实构建、打包、安装、加载、更新和卸载 smoke。测试重点是规格里的状态转换、顺序、权限、取消及恢复，禁止以源码文本断言、mock 回声或默认值快照代替行为。

原生平台矩阵为 Windows amd64、Linux amd64/arm64、macOS amd64/arm64；WASM 同逻辑产物在这些宿主上运行。每个公布目标必须实际执行进程退出、超时及安装恢复场景，交叉编译仅证明可构建。不因当前只有 darwin/arm64 实验而标记其他目标已通过。

race、可控调度和 goroutine/进程泄漏检查覆盖 core/host；解码器与归档读取器 fuzz 覆盖非规范输入。性能报告区分进程内分发、远端调用、冷启动和 WASM 编译缓存，测量 allocation 后再优化，不让 core 分发偷偷序列化所有进程内调用。关键代码和公开契约由非作者独立 review。

## Risks / Trade-offs

- [完整外部包宿主超出 DI 库规模] -> 明确五项 capability 同属交付，按 tasks 依赖推进，不把只完成 core 称为完成。
- [串行实例发生长调用或重入死锁] -> 有界队列、控制独立、调用链检测；不在锁内执行用户代码，长任务采用宿主 task。
- [原生进程绕过能力代理] -> 明确 trusted 模式且可禁用，不能把签名或 stdio 隔离当沙箱。
- [WASI 消息泵与阻塞 host I/O] -> 首版真实 command 会话 smoke 必须覆盖双向调用、取消和无数据等待；reactor 实验不替代它。
- [Windows 文件锁、路径别名与进程树语义] -> 原生实机运行、Job Object、受管快照和 pending removal，不用 POSIX 成功冒充跨平台验证。
- [迁移后代码降级损坏数据] -> 数据版本由宿主确认，迁移提交后禁止自动降级，保留两份代码和恢复原因。
- [包签名正确但信任错误] -> key 与 publisher 绑定由宿主决定，吊销逐次检查；签名不等于安全审计。
- [外部宿主 工件持续变化] -> Adapter 固定快照并报告 drift，不修改宿主事实源让测试通过。
- [公开 Interface 过早固化] -> 在非领域宿主、两个 Runtime、双实例与 外部宿主 seam 全部运行后才发布首个稳定版本。

## Migration Plan

1. 迁移前完成拆分边界的非作者审查，先修订本 change 的交付边界，再移动代码、文档、技能和固定上游参考。
2. 显式更改 module/import、工具路径及文档导航，宿主专属 Adapter/fixture 留在 外部宿主；保留协议标识、签名字节和 timeout 回归。
3. 独立运行通用测试、race、真实 native/WASI 生命周期与依赖验证；单独运行 外部宿主 的原始 ABI、签名包和作者验收，不以框架通过勾选 MVP。
4. 只提交 Cordis，使用 gh 创建 public 仓库并推送；宿主切换到已公开的固定版本，不保留强制 sibling replace。迁出后的新运行证据独立记录，不继承历史平台通过结论。

## Open Questions

- CI runner 提供者与签名私钥保管方式：不影响平台矩阵和 TrustPolicy seam，实施时选择，不在规格中绑定云服务。

### 文档站与 GitHub Pages（2026-10-27）

- 文档站由仓库根 `package.json` 和 `bun.lock` 管理，采用当前稳定的 Astro 7.4.2 与 Starlight 0.42.7；`docs/` 是唯一正文源，`site/` 仅负责导航、主题及构建，通过 Astro glob loader 直接读取原文。不复制现有参考文章到第二棵站点目录，不引入运行时服务、外部搜索账号或 Go 依赖。
- 内容面向中文 Go 插件作者与宿主集成者：先运行真实 counter，再学习 Package / Definition / Entry / Activation / Scope，分别进入作者和宿主路径；公开 API 参考链接至 pkg.go.dev，源码及验收记录链接至 GitHub。现有指向 `../openspec/` 的 Markdown 链接在渲染时重写为仓库地址，源文件继续适合 GitHub 阅读。
- 用户追加现代、美观的要求后，替换初始 VitePress/纸色草稿：冷白、石墨灰与青绿色强调，自托管 Manrope/JetBrains Mono 字体，中文使用平台中文字体。首页以真实安装/运行代码和宿主/插件关系构图建立辨识度，正文保持紧凑侧栏、合理行宽与清晰代码层级。复用 Starlight 的键盘导航、移动侧栏、代码复制和主题切换，不自造组件系统，不修改现有课程资产，不使用营销指标或虚构认证。
- Pages 的部署基路径固定为 `/cordis/`，输出为 `site/dist`。本地搜索使用 Starlight 内置 Pagefind 的静态中文索引，不依赖外部服务。生产构建额外检查生成页链接、资源和锚点，避免把子路径问题留给上线。
- `.github/workflows/docs.yml` 复用既有 Bun 版本及锁文件：push main / pull_request / workflow_dispatch 构建；仅 main 的非 PR 执行可以上传 Pages artifact 并部署。构建 job 只有 contents:read，部署 job 才申请 pages:write 与 id-token:write；按 ref 的 workflow 级并发串行覆盖 build→deploy 全链，避免较旧构建晚完成覆盖新站点，失败构建不部署。使用 GitHub 官方 Pages artifact/OIDC，不创建 gh-pages 分支、不需要自备 PAT。
- 首次在 GitHub 启用 Pages 的 GitHub Actions source。提交工作流后的主分支推送才形成真实远端发布证据；本地构建与启用设置不能报告为已上线。现有未提交改动不代为提交或推送。
- 否决 VitePress 1.6：稳定版仍使用 Vite 5，2.0 仍为 alpha，不符合本次明确的现代稳定工具链要求；否决 Docusaurus：当前 Markdown 文档量与无版本站点需求不值得引入 React 客户端；否决自写静态站点：需要重复实现搜索与无障碍导航；否决 Jekyll：现有 Node/Bun 工具链可直接复用且不需要 Ruby。

### 12. 审计整改与统一公开契约（2026-10-27）

用户明确当前项目未上线，本轮不兼容旧插件；直接同步契约、SDK、工具及示例，不增加旧协议 shim、双轨执行、兼容开关。保留项目既有签名字节定义不是旧插件兼容层；签名输入与摘要仍独立于签名证明，重签可原子刷新。

控制面使用可取消的操作准入，不在普通 mutex 上无期限等待。局部 provider 发布前检查服务与父子所有权组成的动态图；拒绝会使祖先依赖后代的覆盖，不通过无限重启或并发回调绕过。builtin/external 使用统一 Host instance 记录期望配置；durable 提交错误必须区分已提交与未提交，前者同步内存目标并保留错误。

作者通过独立的 ChildScope 能力和 `contract.CreateChild` 创建同包定义子实例，既有 Scope 的原始服务职责不扩张为通用 Context。子请求携带本地 ID、definition/config，宿主以 parent activation 验证并生成 entry 身份；创建只登记不等待激活，避免初始化等待父发布死锁。SDK 和宿主使用同一生命周期请求，子实例拥有独立 activation/进程/绑定与先子后父清理。builtin 父的持久锚点仅记录外部父引用，Restore 要求应用先注册该 builtin；不将 builtin 回调或配置持久化。

严格 typed codec 按有效 JSON 字段、显式 null、数组精确长度和安全整数规则统一校验；自定义 codec 负责其内部值，框架 Nullable 亦使用严格校验。生成 number 使用保留原始十进制的 `json.Number` 表示，不以 float64 舍入合法 const/minimum。原始进程内 Handler 仍支持 JSON；公开 typed 路径基准单独记录，不将 raw 分发当 typed 性能。

候选发现只校验显式来源集合，不下载、启用或自动求解；发现返回逐候选诊断及重复双方来源。级联计划基于 durable enabled 意图与包反向依赖，Host 显式确认后停止相关实例。activation 运行记录与 catalog 分离为宿主持有的有界记录；正常结束写终态，打开存储将遗留运行记录标为 interrupted。

namespace 改为增量持久操作日志，避免每次小写整库；全局 Policy 锁只用于准入/撤权，不覆盖编码、写入、fsync。每次提交重查 owner，撤权与该 owner 的短提交资格有序，其他 owner 不被磁盘阶段阻塞。日志恢复校验顺序、配额和校验和，显式报告损坏；需要压缩时由宿主显式调用，不在热写入路径重写所有数据。

终态接管任务提供显式 Release，宿主持有的 Task 不失效；manager 不承担永久业务历史。CI 覆盖当前声明的平台、conformance、vet、规格和结构向量；只能记录真实执行平台，不把 CI 配置写成已通过证据。

### 12.7 显式动作与存储故障语义

级联计划带 SelectPackage/RemovePackage 动作，不从当前选择猜测；同版本替换不解释成卸载。确认回调只读独立快照，不重入管理队列。所有包、信任、配置与共享数据预检在停用前完成；部分清理后的错误不承诺事务回滚。

作者 ChildInstance 提供 Refresh，远程 Status 是上次应答快照。创建是非阻塞登记，每 activation 限 256 个直接子实例，child 与父使用同 digest；初始化等待自己的子树明确报 reentrant，不靠文档掩盖死锁。Instance 等待参与现有调用等待图。

namespace 使用带序号/hash的有界增量日志，Compact 显式原子替换，单条记录最大64MiB。完整同步后错误是 CommittedError；无法证明回滚或提交时为 RecoveryError 并停止后续访问，重新打开严格验证。这里不承诺断电认证或多写者协调。

独立复核补充：任务 begin/finish/cancel 绑定每次创建的 token，不以可复用逻辑 ID 识别一次任务，避免 Release 后串写。配额持久字段用十进制字符串，namespace/package/key 的 UTF-8 在信任边界校验；维护操作取得存储锁后重新检查取消。typed DecodeData 与协议 Decode 分离：前者容器必须显式 nullable，后者保留 envelope 已声明的 null。注册与 codec 共用 JSON 有效字段，Nullable 递归检查内部类型；原始浮点目标也拒绝超安全整数。已确认取消立即释放监督 timer/goroutine，实例级等待边跨 activation 替换保持直到 revision 完成。

静态与作者子实例共用 entry ID 预留，覆盖预检、持久意图到 engine 登记；失败后释放，不在磁盘等待期间持 Host mutex，也不占用整个清理锁。已 disabled 且非 live 的历史子 entry 无需再次保存，包已卸载时仍能清理父树。namespace 恢复沿用热写路径的增量字节计数，只有 migrate 扫描替换值集，避免每次恢复小写入都重扫全库。

### 14. 使用手册与代码基线（2026-10-27）

保留既有站点设计与 `docs/*.md` 单源。增加组合实操、访问实操、生成服务实操、API 导航、排障及上线维护；已有机制参考保留精确边界，用链接连接到操作步骤。完整 Go 程序直接从教程代码块提取到临时独立 module 编译运行，比较文中的预期输出，不维护第二份示例。生成实操由当前 CLI 生成代码后执行。通用 conformance 复用这个入口，外部 Native/WASM 仍由既有独立作者 module 和真实 Runtime 验证。

中文校订使用 humanizer-zh：只去掉重复、空泛或生硬的表达，不删除关键限制，不改写代码、命令与数据。新增内容先对照实现，再校订正文。

每篇文章的 front matter 添加 `codeCommit`（40 位 SHA）和 `codePaths`（仓库相对文件或目录）。站点在页尾显示“代码基线”及 GitHub commit 链接，明确它不是文档更新时间。`docs:sync` 对每页执行 git 历史及路径检查，并以基线对当前工作树做路径限定 diff，另计未跟踪文件；目录包含新增及删除，历史不存在与非法元数据是错误。没有差异不等于行为已验证，变化只说明需要复核。CI 使用完整历史并输出逐页警告，不因有源码差异阻塞文档发布，也不自动改 SHA。选择非阻塞报告是因为源码变化未必改变文档契约，不能用机械差异替代人工判断。

关联路径采用实际目录/文件，不引入 glob、中央注册表、生成 API 镜像或自动盖章。单页核对后手动更新基线；工作区包含尚未提交的相关变化时，保留原 SHA 和差异，等代码提交并复核后再更新，避免引用不存在的未来提交。网站自身本轮变更也按此规则处理。

构建期变量只在现有 Markdown processor 中替换显式标记的正文、行内代码与代码块，不改写原始 docs，不增加客户端请求或动态渲染。按页 codeCommit 读取对应 go.mod，并通过隔离目录中的 `go list -m -json module@commit` 解析可获取的精确版本；同一构建按 commit 缓存查询，失败显式终止，不回退 latest。CI 的 Node 使用 `.node-version`，Bun 使用 package.json 的 packageManager，先配置 Go 再构建；历史证据、保留 v1 schema 和示例业务版本仍是字面量。原始 Markdown 说明如何以代码 commit 运行安装命令。

站点只服务插件和 Host 开发者。网站构建、部署、文档贡献、代码基线同步与变量维护说明移到 `.github/DOCUMENTATION.md`，只从 CONTRIBUTING 引用，不被 docs loader、导航或 Pagefind 收录。移除 site-maintenance 路由及页尾同步维护入口，保留对开发者有用的代码 commit 链接。

### 15. 单源声明与完整开发体验（待实现）

#### 15.1 契约直接修订，不新增兼容版本

用户已明确本项目未上线，本轮可以直接改。沿用当前 `cordis-go.package/v1`、`cordis-go.contract/v1` 标识，直接修改其 schema、严格 codec、生成器、向量和所有仓内调用者；不提升兼容版本，不保留旧字段、别名、缺字段猜测、双版本读取或迁移 shim。依赖固定旧公开版本的外部宿主须同步更新，不承诺旧二进制或旧 catalog 降级；不静默读取再重写旧签名包。实际数据变化仍须宿主显式迁移和确认，不能借“无兼容”删除用户数据。

签名算法、`cordis-go-package/v1\0` 域及路径/大小/hash 输入布局没有变化理由，继续使用。新的 Manifest 字节产生新的摘要和签名；保留独立签名算法向量，更新被契约变化影响的结构向量，不篡改历史执行记录。此处的 v1 不是稳定发布兼容承诺。

共享 wire 写集另有两个 owner：[isolate-oversized-responses](../2026-10-28-isolate-oversized-responses/proposal.md) 拥有本地普通响应超限隔离，[negotiate-session-frame-budgets](../2026-10-28-negotiate-session-frame-budgets/proposal.md) 拥有双方有效帧预算。已向其 owner 同步“不提升兼容 major、不保留旧握手回退”的用户决定。本 change 不定义预算字段、响应替换或第二套协商；实施先确定单一公共契约及串行合并顺序，仍保留宿主专有 Session 完整替换。

#### 15.2 一份作者声明，复用当前 schema 编译器

采用 schema-first，不是因为 Go struct-first 不可行，而是现有 `schema.Compile/Generate` 已拥有约束、严格 codec 与服务生成。扩展现有作者声明模型支持具名数据类型、配置根和 definition，以及对服务/Requirement 的明确引用；配置-only 合法，不要求伪造服务。不再让作者在 schema、Go 定义、Manifest 三处重写 ID、版本、requires/provides。普通业务仍写 Go；不把业务函数或宿主策略写成一门新 DSL。

作者包源为 `plugin.toml`、`plugin.yaml`/`plugin.yml` 或 `plugin.json`，默认扫描只允许一个候选；显式指定源不意味着忽略同目录冲突。包源声明发行身份、运行目标和本地契约引用，契约可内联或引用包内普通文件；引用只用于显式编译输入，受路径及循环检查，不提供配置 include、网络 `$ref` 或运行时执行。空列表、无数据迁移的初始 0 和唯一 default definition 由编译器补齐。共享一个解析后的声明图，同时生成：

- 严格 Go Config、声明描述与类型化 Start 包装；包装实现既有 Definition seam，按 definition 使用同一 validator，作者业务函数接收 Config 而非再次手写 Decode。
- 服务类型、client/server、provider 和 consumer 描述，供 builtin、SDK 与打包复用；生成的 Requirement 是调用与登记的同一值，不从 provider 版本猜范围。
- 配置的受限宿主 schema 和标准编辑器 schema；校验源不是多个 struct tag 或手工注册表。
- staging 中完整、显式的机器 `plugin.json`。包内运行时只读机器 Manifest，不新增 TOML/YAML wire；源文件不被回写，完整性在生成完成后计算，实际归档文件均受现有摘要覆盖。

`cordis build` 作为现有 CLI 的薄构建流程：使用同一声明图先生成、再调用明确的 Go 构建目标，记录声明指纹、实际 SDK module 版本/能力、目标和产物 hash 的机器构建描述；不执行产物。打包验证描述和精确产物 hash，不凭最新 CLI 声称旧 SDK 支持新协议。任意外部构建须显式提供同样描述，无法验证时失败。描述由工具生成，不成为作者手工维护的第三份 Manifest；它防止标准流程意外漂移，不证明恶意二进制行为真实。现有 pack/sign/inspect 与 MetadataDecoder 继续复用校验、私有 staging 和完整性，不另建包安装器。

#### 15.3 版本与配置职责

| 值 | 唯一来源与规则 |
|---|---|
| 包发行版本 | 作者包源中的严格 `"x.y.z"` SemVer，可有既有允许的 prerelease/build；不复制进业务 Go |
| 服务 provider | 作者声明的整数 `1` 表示 1.0，或字符串 `"1.2"`；不是浮点，不接受补丁位 |
| 服务 consumer | 显式 service/major 和闭合 minor 范围；不套用包 SemVer 区间，不自动扩大 |
| 包依赖 | 复用当前 comparator Range 或半开 Min/Max，诊断显示实际采用的约束 |
| schema / 协议 | 工具格式固定于当前修订；协议能力来自实际构建 SDK 描述，不由作者重复填 |
| 数据版本 | 初次未迁移可省略为 0；迁移后显式保留当前值，只有数据格式变更才推进，不随发行版本重置 |
| desired / applied revision | 框架内部维护，不要求作者配置或同步 |

Manifest 移除包级 ConfigSchema，改为每个 DefinitionSpec 唯一携带配置契约；builtin Definition 引用同一生成描述。Host 在 Enable、Update、Replace 和子实例登记的共同预检边界按 definition 解析和验证，完成后才提交持久意图或停止旧激活。缺省输入统一转为 `{}`，仅针对真正省略，显式 null 保留；必填字段不会因缺省而绕过。schema/数据失败不改变旧状态；通过预检后的业务初始化仍可能失败，不宣称能前置执行所有业务检查或自动回滚副作用。

#### 15.4 统一文件解析与编辑器投影

增加单一 `config/` 文件边界，仅拥有格式选择、受限语法树到 JSON 数据模型的转换、位置映射与错误；依赖 `contract`，不依赖 Host、Runtime 或 schema 生成器。`schema/` 及 CLI 复用它，再交现有 schema 校验；Host/core 继续消费规范数据，不在生命周期锁内读文件。依赖选择和许可证在实施时依据当前主文档审查，优先已有解析器；不能以 YAML `map[any]any` 或 float64 中间值丢失结构及数值。

格式由显式参数或已知扩展名确定，不探测失败后轮流 fallback。对源字节、深度、集合和引用设有界限制；同一声明图的本地引用展开共享累计 1 MiB 字节预算（包含中间复制，在分配前计费），不能凭重复引用或多个 definition 放大绕过，解析时拒绝重复键、无效 UTF-8、尾随 JSON/YAML 文档、非字符串键、YAML alias/merge/自定义 tag。保留数值词法与精确值；TOML 数字分隔符及进制到 JSON 的转换采用精确整数/十进制规则，不经 float64。共享数据 codec 特判 json.Number 的数字 token、有限性及安全整数限制，拒绝引号字符串和非法词法；合法高精度小数保持可往返。

推荐顺序按可表达能力而不是“一种格式包办”：对象型、人手维护优先 TOML；需要 null、非对象根或更深层结构时推荐 YAML；自动化交换用 JSON。TOML 无 null，不把省略、空串或 `"null"` 当 null；三种格式只接受 JSON 模型内的值，TOML/YAML 原生日期时间要求改为引号字符串，非有限数拒绝。实例配置不填业务默认、不隐式环境合并、include/watch 或回写注释。错误包含用户提供的来源名、JSON Pointer 和可获得的行列；可信本地路径与对外安全投影分离。

编辑器导出为 JSON Schema 2020-12：nullable 转成 null 分支，闭合对象显式 additionalProperties:false，tagged union 转 oneOf + required const，不输出私有 discriminator/nullable 假装标准。int64/uint64 十进制及 byte/base64 约束尽量转换成标准 pattern/边界，保守处理 Go RE2 与标准 regex 的差异；不等价项输出说明而非错误映射或假通过承诺。提供 JSON 编辑器文件关联、YAML language-server modeline 和 Taplo `#:schema` 示例；不往闭合业务对象插入 `$schema` 再特殊剥离，注释仅由文件语法处理。对编辑器无法验证的完整字节/数值/正则约束，运行时仍是权威。

#### 15.5 生成器、共用能力与作者测试

共用 Caller/Registrar 等窄接口归 `contract`，生成文件引用而非逐文件定义；生成入口可一次接收多个契约输入，完整检查输出包符号再写文件。普通叶子用 string/bool/共享数字与 bytes 类型，不因 request/response 路径不同生造命名类型；约束放在父 codec，显式具名约束根保留独立 codec。Optional/Nullable 复用共享类型，联合构造器填 tag；不以少写转换为由放松缺失/null/零值和边界校验。

client 构造接受生成的消费者描述，其中包含同一 Requirement 与方法契约的本地引用/确定指纹；生成描述和 Manifest 引用同一声明身份，client 比对自己的生成契约与该身份，并验证服务名、major 和范围。当前纯 Requirement 没有方法信息，不能单独用来声称校验数据契约。指纹复用规范化声明描述，不把 provider 新 minor 的整体指纹强制等同于 consumer，也不自动证明跨 minor 语义兼容；数据仍在各自 codec 边界验证。默认精确版本可供简单场景，但不得覆盖实例选定范围。Host 提供 realm-bound Caller/Emitter 视图，沿用原路由/可见性/授权/等待图，不建立第二个 engine。宿主调用、贡献、任务和子实例提供小结构接口及 helper，按真实能力准入，不要求 `scope.(*sdk.Client)`，也不把 Scope 扩张为无所不包的 Context。

公开业务测试 harness 复用真实 definition/codec 和受管生命周期，可显式注册假业务 port 并观察配置、服务、宿主调用和清理，不要求作者手写 initialize/net.Pipe。它不假装验证 wire 或 native/WASI 隔离；真实协议、超时、两种运行时及独立 module 的 conformance 保留。SDK 提供标准 main/信号/退出处理与可返回错误的 Serve 低层入口；stdout 始终留给协议，业务错误不自动重试。

#### 15.6 生命周期、管理与恢复

修复 SDK 清理为每项单独 recover、LIFO、至多一次和错误聚合，复用 `contract.Group` 的已有机制与独立清理 context，保留清理期间绑定能力。启动 ctx 在 Start 返回后结束，activation ctx 继续有效，builtin/SDK 一致。显式受管工作是作者主动选择的并发，使用独立工作执行位置，不占服务/事件串行位；共享业务状态由作者同步。工作由 activation 取消并等待，错误/panic 进入现有失败路径；不是持久 Task。资源接管 helper 在登记失败时关闭资源并合并错误，不把每次创建后手写 Defer 变成作者义务；裸 goroutine 和不合作 builtin 不受强杀承诺。

外部 SDK 的 Start 成功不等于 Host 最终发布。直接扩展当前 `PluginSession` 与 core lifecycle hook 的发布/退役操作，通用 Session 用绑定 activation 的 `activation.publish`、`activation.retire` 控制请求/确认；这些通知不进入业务串行位，不增加兼容版本。Host 完成所有贡献/provider/环校验并提交发布后才发送 publish；SDK 单调经过 initialized、published、retiring、closed，publish 确认才启动受管工作，重复同状态通知幂等，retiring 后任何迟到 publish 不启动工作。本地发布提交不等于启动完成：只有当前 activation 和 revision 的 publish 确认成功，才能推进 Active、applied 和启动/更新等待者；确认期间不开放普通调用和依赖就绪。通知或确认失败使激活进入明确失败/退役，不留下假成功；Stop、替换或退役后的迟到确认不得推进 applied 或复活工作，等待确认不持协调锁。Host 关闭普通准入后，在等待消费者清理前通知 retire；SDK 先取消普通及工作 context 并确认通知，再于自身 shutdown/资源清理前等待受管工作退出。清理继续使用独立绑定 context，不能用取消通知提前释放 provider 资源。当前 Invocation 显式携带必需的 cleanup 布尔分类，仅 Host 从 core 可信清理 context 生成；SDK 对此保留独立请求 context 与 parent lineage，退役仅阻止普通调用，不拒绝已持有绑定的清理。完整自定义 Session 必须映射相同生命周期操作；未支持受管工作的适配在接纳该能力前明确拒绝，不注入 generic envelope 或暗中改 ABI。

管理准入只覆盖校验、预留和控制意图提交，Resume、Replace、Stop 等不在全局准入内等待依赖或用户清理。关联 entry/包及树使用已有预留、登记代次和过期结果检查；配置 revision 提交后释放短期预留，让新配置和 Stop 可取代旧等待，替换/cascade 则预留所有目标和依赖所有权树到完成；控制顺序、Close 取消及不同时发布旧新 activation 仍保持。阻塞在某个条目的操作不妨碍注册它等待的 provider 或管理无关实例。

最小公共查询包括按 entry ID 取当前句柄和只读清单；旧句柄不转向新登记。状态区分持久 Enabled、当前 runtime 状态和临时停止，等待原因结构化包含要求范围、可见 provider 与不兼容原因，不暴露不可见 realm 的具体数据。Stop 是当前宿主临时操作，Disable 是持久停用，Remove 是移除登记/记录；不靠 API 文档一句含糊“停止”掩盖重启差异。

Restore 返回逐条报告及可检查的聚合错误，按父顺序尝试登记；某父失败只阻塞该子树。重复调用只补齐缺失登记，匹配 immutable identity、realm、parent、definition 与选定 PackageRef；已匹配目标不重启，Failed 不隐式 Retry，当前宿主内临时 Stop 不被 Restore 撤销。冲突需显式管理操作，不悄悄覆盖；真正重开宿主按持久 enabled 意图建立新 activation。配置更新与恢复预检仍遵守原提交错误语义。

#### 15.7 access 与宿主分发由原机制直接完成

当前 Grant 句柄留在 Policy；增加受信任 owner + capability 的撤权入口，以当前 grant/token 线性化撤销并通知在途调用，不让 Authorize 回调创建随后被覆盖的第二个 Grant。TaskManager 的 Release 扩展至所有终态任务，宿主显式决定保留/释放，活跃不可删，不静默淘汰结果；旧句柄保留独立终态 snapshot，释放回收计数及结果预算，复用 ID 仍须新 token，旧 begin/finish/cancel 不能串写。

类型化宿主能力/贡献 helper 和标准 namespace broker/client 复用 `contract.EncodeData/DecodeData`，统一 bytes codec，owner/namespace 从受信任会话绑定取得，插件不能选任意 namespace。业务 quota、授权 scope、迁移/删除确认依旧宿主提供。收到有效存储应答时以 tagged outcome 表达 missing、denied、quota、未提交、committed 和 recovery-required；本地 errors.Is/As 与跨会话投影保持对应，不把 CommittedError 或 RecoveryError 丢成可盲重试的 internal。若请求发出后因取消、撤权或断链没有确定应答，typed 客户端返回带原原因的 submission-unknown，而非伪造未提交或 storage recovery-required；只有能证明未发送/未提交时才可使用相应结果，迟到应答仍拒绝，不自动重试或补建持久操作查询系统。直接修订当前未稳定载荷及向量，不新增兼容版本或旧结果 fallback，也不以结果 envelope 绕过授权和提交点。

根 Host 公开可显式委托的标准 guest dispatcher，自定义方法按 method 精确分派；处理器失败不表示“未处理”，不 catch-all 尝试下一个。保留完整 Session/HandleGuest 替换给宿主专有 ABI，不能隐式套第二层 envelope。受信任诊断出口直接复用 `access.Sink`，在 PublicError 投影前记录可用本地 cause、身份、调用和 stage；Runtime 仅显式启用有界脱敏 stderr。来自对端的安全 code 不会凭空变回原 guest error。Sink 失败同步合并返回；异步失败进入宿主持有的有界状态/查询，保留首因，不递归调用已失败 Sink，不改变 wire 的关闭规则。

#### 15.8 CLI、交付顺序与取舍

所有命令采用一致帮助及退出码，version 使用真实 build/module 信息。增加原子 `generate -output`（先解析、冲突检查、生成及 gofmt，成功后替换），stdout 只作为显式输出模式；不声称能保护用户事先执行的 shell 截断。`run` 默认使用有限、可配置且独立于业务调用的 startup timeout，状态变化时在 stderr 输出有界等待诊断，调用结果仍为纯 stdout；config/input 文件共用 loader，仅一个输入可占 stdin，显式格式与 flags 冲突报错。`init` 只生成最小作者声明、业务入口与测试，不造构建系统、默认信任或自动 native 许可；`build` 复用上述单源生成流程。

实施按任务第 15 组推进：先冻结本轮单一契约和失败语义，经非作者审查，再改公共模型/codec；随后文件解析与生成、Host/core、SDK/access 分离写集实施，最后同步 CLI/示例/文档并走真实端到端。共享契约和两个消息 change 由 Integrator 串行合并。每组自带当前行为测试；最终 conformance、互操作和平台报告不能用本轮调查复现或历史通过替代。

风险与取舍：

- 单源生成会增大工具职责，但删除 Go/Manifest/输入间重复；复用现有 compiler，而不并行维护 struct-first。仅为三种文件格式审查解析依赖，不引入配置管理框架。
- 去掉全局长等待后竞态边界更清楚但实现更细；用同 entry/树的预留与代次保护，不用全局锁掩盖顺序，也不另建任务队列系统。
- editor schema 不是运行时 schema 的简单拷贝；保守投影无法表达约束，保持运行时权威，不承诺编辑器等价验证所有输入。
- 直接修订将使旧输入和固定旧版本的消费者不兼容；同步源码和 fixture，不加兼容版本或 shim。旧持久存储无法读取须明确失败，处理真实数据前由宿主确认，不自动清库。
- 标准流程能防止意外声明漂移，不能鉴定恶意二进制、强杀不合作 builtin 或回滚业务事务；便利入口不改变这些信任边界。

自动 SDK 构建证明读取有效 GOFLAGS，遇到 Go -overlay 明确拒绝并要求外部流水线显式构建描述；物理依赖的能力文件不能冒充 overlay 实际编译输入。生成文件不发射固定包级 DeclarationFingerprint，统一按 definition/service 命名，允许 CLI 分次生成无关声明到同包。
