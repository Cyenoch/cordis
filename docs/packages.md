---
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - packagehost
  - contract/types.go
  - host.go
  - cascade.go
---

# 外部插件包

`packagehost` 管理包和持久状态，不依赖根包或 core，不执行插件，也不替宿主停止实例、迁移数据。安装记录、显式版本选择和 entry 配置保存在同一 catalog 根中；安装新版本不会改变选择。发布步骤见[上线维护](operations.md)。

## 作者流程

作者包源只保留一份 `plugin.toml`、`plugin.yaml` / `plugin.yml` 或 `plugin.json`。优先使用 TOML；需要 null 或复杂结构时可使用 YAML，JSON 适合机器交换。即使显式指定文件，同目录存在多个候选仍报错，不按扩展名优先级选择。版本是完整、严格 SemVer（不带 `v`）；禁止 latest 和网络解析。运行类型只接受 `wasm` 或 `native`；native 必须显式列出每个 OS/Arch，绝不回退执行形式。

最小 WASM 作者源：

```toml
id = "example.counter"
version = "0.1.0"
publisher = "example"

[runtime]
kind = "wasm"
entry = "plugin.wasm"

[[contract.definitions]]
id = "counter"
```

`contract` 是内联的 `schema.Definition`，可包含 `types`、`definitions` 和 `services`；格式标识可省略。需要拆分时，用互斥的 `contracts = ["contracts/counter.toml"]` 引用包内契约文档。契约文档可用 `contracts` 列表继续组织集合，所有路径都相对包根，不相对引用文件；拒绝绝对路径、父目录、网络、链接和循环，整个声明图最多 1 MiB、引用深度最多 32。数据 schema 中的本地类型引用、约束和循环由同一 schema 编译器校验，不提供网络 `$ref` 或运行时 include。

编译补齐空列表、唯一默认定义及初始 `dataVersion = 0`。多个 definition 必须明确 `defaultDefinition`；每个 definition 有自己的配置 schema，包级 `configSchema` 不合法。配置未声明时是严格空对象。发行版本只写包源，不复制到业务 Go；数据迁移之后显式保留 `dataVersion`，仅数据格式变化时推进。provider 的 `version = 1` 表示 1.0，`version = "1.2"` 表示 1.2；拒绝浮点和补丁位。consumer 必须显式给出闭合 minor 范围，不从 provider 版本推断。

```go
author, err := packagehost.LoadAuthor(sourceDirectory)
if err != nil { return err }
// 同一解析结果还可供 CLI generate 使用；调用者拥有 writer。
if err := author.Generate(writer, "main"); err != nil { return err }

descriptor, err := packagehost.Build(ctx, sourceDirectory, packagehost.BuildOptions{
    GoPackage: ".",
    GOOS: "wasip1",
    GOARCH: "wasm",
})
if err != nil { return err }
_ = descriptor // 同时原子写入 sourceDirectory/build.json
```

`Build` 必须明确指定一个 Go package 和 GOOS/GOARCH；WASM 只接受 `wasip1/wasm`。`Output` 默认取声明中的精确目标路径，显式值必须与声明一致。生成默认使用 `GeneratedPackage: "main"`、`GeneratedOutput: "cordis_gen.go"`；输出相对包根，先验证并格式化全部代码，再原子替换生成文件，然后调用 Go 构建、记录产物摘要，从不运行产物。重复构建不同 native 目标时，只有声明、生成摘要与 SDK 证明相同才保留其他目标记录；Pack 要求所有声明目标齐备。

`build.json` 的 schema 为 `cordis-go.build/v1`，记录声明指纹、生成代码摘要、生成包名/路径、实际 SDK module 版本与 replace、SDK 协议/特性证明及每个目标的 SHA-256。SDK 取自目标 Go package 的实际依赖及其中的 `sdk/capabilities.json`，不是运行 CLI 的 SDK。native 还比对产物内嵌的 Go module/目标信息；Go 的 buildinfo 不支持 WASM，因此 WASM 依赖构建描述和精确产物摘要。这里防止标准流程中的意外漂移，不鉴定恶意二进制。

外部流水线可调用 `PrepareBuild(ctx, dir, options)` 获得同一 `Author` 与尚无目标的描述；该调用只生成和解析实际 SDK，不声称产物已构建。流水线完成构建后显式填写 `Targets`，再调用 `WriteBuildDescriptor(dir, descriptor)` 原子记录证据。无法自动读取 SDK 能力时，外部构建必须显式提供实际 SDK 证明和同结构描述，不能默认使用 CLI 能力。Pack 重新生成并比对声明、生成代码与目标 hash，任何漂移均在替换旧归档前失败。

当前机器 Manifest 使用 `contract.Manifest` 和 `cordis-go.package/v1`，只能位于 `plugin.json`，每个 definition 必须显式具有 `configSchema`。它不包含作者契约，可继续直接 Pack，不需要构建描述；不会在校验失败后尝试旧字段或作者语法。已有宿主格式可通过下面的受信任 metadata decoder 接入，不要求重写原始 `plugin.json`。

```go
pkg, err := packagehost.Pack(ctx, sourceDirectory, archivePath)
if err != nil { return err }
pkg, err = packagehost.Sign(ctx, archivePath, keyID, privateKey)
if err != nil { return err }
_ = pkg.Ref // 内容身份：ID、version、digest
```

Pack 在私有临时目录生成不可变副本；作者源先通过构建一致性检查，仅在副本移除源候选并生成完整机器 `plugin.json`，随后严格验证 Manifest 与配置 schema、重建完整性表，按路径顺序生成 ZIP。源目录不被回写；运行时只读取机器 Manifest，不引入 TOML/YAML wire。生成后的 Manifest、构建描述、契约文件和其他实际归档文件均受完整性覆盖。Sign 对已有 archive 再校验后原子替换签名。它们是 CLI 和宿主共享的唯一打包实现。native 包可包含跨平台产物；实际安装和 Snapshot 时要求存在本机精确匹配产物。ZIP mode 不作为执行权限来源；native runtime 应只对本次私有 Snapshot 中选中的 artifact 设置执行权限。

服务/requirement 名是逻辑标识，长度上限 512；贡献 ID 上限 200。二者允许连续点以保留宿主合法 provider ID，不把它们解释为文件路径；字符仍仅限 ASCII 字母数字及 `._-/`，拒绝首尾或重复斜杠。package/publisher/definition/slot 名继续使用原 200 长度与连续点拒绝规则；归档路径仍执行下述完整路径校验。不会因逻辑标识放宽文件系统边界。

`integrity.json` 为 `{"files":[{"path":"plugin.json","size":123,"sha256":"小写十六进制"}, ...]}`。覆盖除 integrity.json/signature.json 之外的所有常规文件，不接受额外或缺失文件。签名输入为 ASCII `cordis-go-package/v1`、NUL、uint32 大端条目数，再逐项连接 uint32 大端 UTF-8 路径字节数、路径、uint64 大端大小、32 字节 SHA-256。条目按 UTF-8 字节排序。digest 是该输入的 SHA-256，Ed25519 直接签该输入；不签不稳定的 JSON 文本。`signature.json` 为 `{"keyId":"...","signature":"标准Base64"}`。

## 宿主原始元数据与依赖区间

`packagehost.MetadataDecoder` 的签名为 `func(context.Context, json.RawMessage, fs.FS) (contract.Manifest, error)`。输入是已受大小/路径/链接限制的私有 archive 快照及原始 Manifest 字节，不是可变源目录；decoder 只做宿主拥有的元数据投影，禁止执行插件。投影仍经过 `ValidateManifest`，不会绕过完整性、签名、publisher/ID 授权或平台检查。

Store 使用 `OpenWithOptions(root, packagehost.Options{Trust: trust, Limits: limits, Decode: decoder})`；打包、签名和只读检查对应 `PackWithDecoder`、`SignWithDecoder`、`InspectWithDecoder`，各自在原有参数后接相同 decoder。Facade 通过 `cordis.Options.DecodePackage` 传入同一投影逻辑。缺省 decoder 保持通用格式；不能打包时采用一种投影、安装时采用另一种。原始宿主字节可保留于 `contract.Manifest.HostMetadata`，参与内容校验但不改写已签名条目。宿主自行维护原始 Manifest 的投影和端到端验收。

包依赖有两种互斥表示：`Min`/`Max` 是含下界、不含上界的 `[Min,Max)`；`Range` 是以空白分隔、全部必须满足的严格比较器交集。`packagehost.VersionSatisfies(version, expression)` 接受 `>=`、`>`、`<=`、`<`、`=`、`^`、`~`，每个操作符必须紧接完整 SemVer，例如 `>=1.2.0 <2.0.0`。`^` 上界递增第一个非零核心段（全零时为 patch），`~` 上界递增 minor，均重置后续段并排除上界；如 `^0.2.3` 为 `[0.2.3,0.3.0)`，`~1.2.3` 为 `[1.2.3,1.3.0)`。使用严格 SemVer 顺序比较，build metadata 不改变优先级，预发布也按该顺序比较，不额外套用 npm 的隐式排除规则。拒绝空表达式、裸版本、缩写版本、`v` 前缀、通配符、OR、连字符区间、tags 和混用 Range 与 Min/Max；没有隐式拉取或宽松转换。包依赖范围与服务的闭区间 minor 绑定不是同一种契约。

## 信任与开发导入

`TrustPolicy.Keys` 绑定 keyId、公钥和 publisher；`Publishers` 将 publisher 映射到精确 plugin ID 列表。签名有效不等于有权发布其他 ID。吊销 key 或 publisher 后，下一次 Snapshot 会拒绝执行。`UpdateTrust` 替换 Store 内部独立副本，不靠修改调用者 map 生效。

默认拒绝未签名包。开发许可只接受 `DevDigests[digest] == true` 的具体内容，仍要求 publisher 对 plugin ID 的授权。可先 Pack 或 `Inspect(ctx, archive)` 获取经格式/完整性核验的 digest，再明确授权该 digest 后 Install（或 ImportDirectory）。Inspect 不授信、不安装、不执行。许可接受的未签名 ZIP 明确标记 Development；同一许可不能允许修改后的内容。目录先复制再验证，绝不直接执行可变开发源文件。

已安装或保留数据的 ID 不允许新 publisher 自动接续。`PublisherChanges[id][newPublisher] = true` 是宿主显式批准的迁移；仍需正常 key/publisher/ID 授权。卸载但保留数据时保留 publisher 与数据版本记录。

同 digest 重签后再次 Install/ImportDirectory 会先按当前策略验签，再原子刷新 catalog 的 `signatures[digest]` 与来源/开发标记，不覆盖共享 content。CheckSelect 使用当前证明；Snapshot 将该证明写入本次私有副本并再次验签。证明不是永久授权，后续吊销仍生效。未提交刷新继续使用旧证明，已提交刷新在重开后保留；原签名字节和 digest 算法不变。旧 catalog 尚无该摘要的证明记录时，仍验证 content 自带签名；重新安装后以 catalog 证明为准。

## 候选集合发现

```go
type Source struct {
    Path string
    Directory bool
}
type Candidate struct {
    Package Package
    Source Source
    Err error
}

func Discover(ctx context.Context, sources []Source) ([]Candidate, error)
func (s *Store) Discover(ctx context.Context, sources []Source) ([]Candidate, error)
```

每个 Source 明确表示一个 ZIP 或一个开发包目录，不递归搜索包集合。结果保持输入顺序；每个来源的读取/格式/完整性错误放在 `Candidate.Err`，不因一个坏包丢弃其他结果。同 ID/version 不同 digest 时，所有冲突候选都有包含双方来源及摘要的诊断；相同 digest 的重复来源不冲突。取消返回已完成的部分结果及 context 错误。成功结果的 `Package.Directory` 为空，不可作为执行源。

顶层 Discover 使用默认配额和通用 Manifest；Store 方法复用其配额与 MetadataDecoder。两者只在临时私有副本中检查，不安装、不选择、不执行、不授信，也不保证本机平台或宿主协议可启用。开发目录缺 integrity 时仅在副本生成；来源不会被写入。后续 Install 和 Snapshot 仍执行各自的平台与信任校验。

## Store 使用约束

```go
store, err := packagehost.Open(root, trust)
if err != nil { return err }
defer store.Close() // 实际宿主必须处理 Close 错误
pkg, err := store.Install(ctx, archivePath)
if err != nil { return err }
err = store.Select(ctx, pkg.Ref, packagehost.ChangeOptions{})
if err != nil { return err }
snapshot, err := store.Snapshot(ctx, pkg.Ref)
if err != nil { return err }
// 只将 snapshot.Package.Manifest 与 snapshot.Directory 交给 runtime。
// activation 停止、进程/文件句柄关闭后处理 snapshot.Close() 错误。
```

- `OpenWithLimits` 公布并收紧限制；默认压缩 64 MiB、单文件 64 MiB、展开总量 256 MiB、10,000 条目。在构建 ZIP 完整索引前扫描实际 central directory 记录并限制条目数，同时限制 EOCD/ZIP64 宣称的数量；不能用伪造或回绕计数绕过内存限制。实际展开读取也计数。metadata 每份最多 1 MiB。manifest 与 entry 配置各自保持 JSON 深度 32、数组长度 10,000 的叶文档限制；catalog 额外预留三层封装深度，不占用叶文档预算。每次提交在替换根之前用恢复时相同的规则校验已编码候选根，禁止提交重开无法读取的 catalog。
- 路径为 NFC 相对 POSIX 路径；拒绝父目录、绝对/驱动器/UNC、反斜杠、控制字符、Windows 保留名、尾点空格、链接、重复及 Unicode 大小写折叠冲突。目录源使用 `os.Root` 限制打开范围；开发源的硬链接也拒绝。输入先检查 regular file，打开后再检查；Unix 使用 nonblocking/no-follow 打开，避免检查后替换为 FIFO 导致永久阻塞。
- `Get` 与 `Installed` 用于检查元数据，不能用其 Directory 直接启动。`Snapshot` 仅接受显式选中的引用，重新复制、核验完整性、信任、平台与必需依赖后返回一次执行私有目录。
- `Preflight` 检查已选定的必需版本范围和循环；可选缺失/不兼容返回诊断，不阻止启用。包依赖满足不代表运行服务就绪。
- `Entries` / `SaveEntry` / `DeleteEntry` 持久化配置。SaveEntry 和切换版本都先验证定义及配置 schema；未知字段/重复 JSON key 会在解码边界拒绝。
- `RegisterParent(ctx context.Context, id string) error` / `UnregisterParent(ctx context.Context, id string) error` 是 Store 方法，只在 catalog 的 `externalParents` 保存宿主父 ID。锚点参与父图校验，不得与真实 entry 重名；任何子 entry（包括 disabled）仍引用时不能注销。重复注册/注销幂等。不会持久化 builtin 回调、配置或运行状态，Entries 不返回锚点；宿主必须先重建 builtin 再恢复外部子 entry，删除子 entry 后才能注销锚点。
- `ChangeOptions.InUse` 必须完整列出当前活跃引用，旧 activation 未停止不能切换/卸载。`Enabled` 必须完整列出显式级联停用完成后的启用包 ID；反向依赖不兼容拒绝切换，不能静默卸载依赖者。
- `ReverseDependencies(ref contract.PackageRef, enabled []string, action ChangeAction) ([]string, error)` 是只读 Store 方法。action 显式选择 `SelectPackage` 或 `RemovePackage`，同版本选择不等于卸载。它复用递归 Preflight，返回受阻 enabled 包 ID 的去重、排序传递闭包（不含 ref.ID），忽略 optional 依赖；既有必需依赖故障也会出现在结果中。它不授予级联权限、不决定实例停止顺序。宿主须明确授权、停止对应实例，再按实际剩余 Enabled 调用 Select/Uninstall。无效引用或已关闭 Store 返回错误；非选中版本的直接卸载不影响当前选择。
- 替换流程在停止旧 activation **之前**调用 `CheckSelect(ref, options)`：检查新内容完整性与当前信任、正反向依赖、schema 和数据版本，不提交且忽略 InUse。停旧实例后再调用 Select（重新检查并提交），避免不兼容请求先造成停机。若迁移已经发生，必须先完成下面的独立确认，再调用 CheckSelect 或其他版本操作。
- 宿主每次提交业务数据迁移后，必须**立即**调用 `ConfirmDataVersion(ctx, id, version)`，成功记录事实之后才能进行版本预检、选择或执行。此 API 不要求新包已安装或受信任，不改变代码选择，数据版本只能递增（同版本重复确认无副作用），有意忽略 context 取消：取消不能撤销已发生的宿主数据变化。确认失败时宿主必须停止相关版本操作并修复持久化问题；不得恢复旧代码。为兼容原有调用，`Select` 收到 `MigrationCommitted: true` 时会先独立确认 `DataVersion`，然后才检查目标、信任、依赖、配置、InUse 与取消；即使后续选择失败，确认事实仍保留。该选项仍要求目标 manifest 的 DataVersion 匹配，但匹配失败不能撤回确认。`CheckSelect` 本身只读，不替代确认 API。
- 已确认的数据版本立即阻止旧版本 `Snapshot` 与选择，并在重开后继续生效。选择失败不自动回滚选择，原引用可能仍是 selected 但已不可执行；宿主需显式选择兼容代码或执行授权的数据恢复，Store 不运行迁移、不猜测 rollback。未改变数据格式时旧包仍保留，可显式选择恢复。
- `DataDirectory` 使用 SHA-256(ID) 独立 namespace，避免具有斜杠的 ID 嵌套或大小写文件系统冲突。Uninstall 默认只删代码；`DeleteData: true` 表示宿主已取得明确授权，多版本共享数据时拒绝删除。
- Windows 文件被占用时 Removal 返回 Pending/Reason，不假报删除成功；`PendingRemovals` 可查询，`RetryRemovals` 和下次 Open 重试清理。

## 提交、取消与恢复

Store 持有跨进程独占锁直到 Close，同实例提交串行化。内容在同文件系统 staging 写入并校验，文件与目录同步，事务记录先落盘，再通过原子替换 catalog 根提交。catalog 根是唯一提交点；孤立内容不是已安装包。

一般安装/选择在提交前取消不改变该操作的已安装集合和选择；catalog.next 同步后、rename 前再次观察取消。已经发生的迁移确认是独立提交，不受此取消检查影响。提交后即使 context 已取消也返回实际已安装 Package，不伪报取消未发生。`CommitError` 明确表示相应 catalog（或下述 runs 文件）已替换但后续同步或清理失败，调用者不得当作未提交盲目重试。对 `ConfirmDataVersion`，该错误表示确认已过提交点而非迁移未发生；对兼容选项触发的确认，`Select` 会包装并返回它，此时新代码尚未选择。通过 `errors.As` 识别错误，保留存储诊断并停止相关版本操作，恢复后检查实际选择，切勿把它视为可以重启旧代码的依据。

重开清理未提交 staging、孤立内容、上次执行快照及事务残留，保留已提交安装和选择。旧运行记录按下述规则中断；宿主必须重新创建身份、重新 Snapshot、再启动。原生 runtime 为受信任模式，不声明抵御拥有相同用户完整权限的外部进程或管理员并发修改。

## 运行记录

```go
type RunRecord struct {
    Owner contract.Identity
    State string
    StartedAt time.Time
    EndedAt time.Time
}

func (s *Store) BeginRun(ctx context.Context, owner contract.Identity) error
func (s *Store) EndRun(ctx context.Context, owner contract.Identity, state string) error
func (s *Store) Runs() ([]RunRecord, error)
```

BeginRun 在启动前持久记录 `running`，EndRun 在实际结束后记录 `stopped`、`completed`、`failed`、`canceled` 或 `interrupted`。owner 的 Package/Entry/Activation 使用有效名称，Digest 为 64 位小写十六进制或 `builtin`。身份完全匹配才可结束；重复相同状态幂等，冲突终态和仍保留的 activation 复用显式失败。它不要求当前安装/选择或 entry 存在，不是执行授权、进程探测或业务事务日志。

独立 `runs.json` 原子替换，取消边界与 catalog 相同，但两个文件不是联合事务。Open 在返回 Store 前把遗留 running 持久标为 interrupted。最多保留最近 256 条 terminal，按更新顺序淘汰；running 不淘汰，达到 10,000 条时拒绝新的 BeginRun。读取文件上限 16 MiB。Runs 返回独立副本，按更新时间顺序排列，最旧在前。Close 不假报正在运行的实例已停止；宿主必须先结束实际运行并 EndRun，重开发现的残留才算 interrupted。被淘汰记录不再用于去重，宿主仍须为每次启动生成全新 activation ID。

行为测试使用真实临时目录和 ZIP，位于 `//go:build integration` 测试；包括无效路径/链接、大小写与 NFC 冲突、配额、签名/吊销/身份不匹配、开发快照、恢复、反向依赖和数据降级。另有 SemVer/Manifest/path fuzz 入口。复现入口为 `go test ./packagehost`、`go test -tags=integration ./packagehost` 及对应 race/fuzz；集成负责人已执行的结果、源码修订和平台边界以 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md) 为准。

## 显式级联

`Store.ReverseDependencies(ref, enabled, action)` 的 action 为 `packagehost.SelectPackage` 或 `RemovePackage`，不会根据 ref 是否当前选中猜测意图。同版本重新选择不等于卸载。结果是排序去重的必需反向依赖包闭包，不包含可选依赖和目标自身。

`Host.PlanCascade(ctx, ref, action)` 返回目标、动作和受影响 entry 的独立快照。`ReplaceCascade` 和 `UninstallCascade` 在任何停用前完成包/信任/配置/数据预检，再要求 `ConfirmCascade` 明确批准；缺 callback 或拒绝不会停用。卸载计划包含仍启用的目标 entry，替换只关闭阻塞反向依赖，目标由正常 Replace 重建。回调不得重入 Host 管理操作；修改回调收到的计划不会改变执行计划。已明确停用的反向依赖不会被偷偷重启；取消/清理失败可能留下部分停用，操作不是业务事务回滚。

自动 `Build` / `PrepareBuild` 不接受有效 `GOFLAGS` 中的 `-overlay`，因为物理 SDK 能力文件不能证明 overlay 编译输入。使用 Go overlay 的外部流水线须显式记录实际编译输入对应的构建描述及摘要，再通过 `WriteBuildDescriptor` 交给 Pack 验证，不套用磁盘原 SDK 能力。
