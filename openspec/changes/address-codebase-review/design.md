# Design

## Context

审查对象为提交 164340f 的全部 Go 代码（不含 `third_party/` 与 `.local/`），约 4 万行，其中测试约 1.7 万行。负载假设：单宿主进程、数十个插件、管理操作稀少且经单一准入槽串行，插件调用是热路径。审查时 `go build`、`go vet` 与 `go test -race ./...` 在 darwin/arm64 全部通过；未运行 `-tags integration` 与 `conformance/run.sh`。

## Decisions

### 缺陷修复

- **在途计数**（`wire/session.go` 的 `receive` 与 `handle`）：收到 `cancel` 帧时把对应 `incoming` 标记为已取消并递减有效计数；准入只比较有效计数，handler 正常返回时递减，已取消的 handler 返回时只删除记录，不重复递减。迟到的 handler 不再占用对端准入额度，不放宽对真实超额的拒绝。
- **替换不改启用意图**（`host.go` 的 `replace` 与 `disableOne`）：拆出只退役运行树、不写 `SaveEntry` 的路径供 `replace` 使用；`Disable` 与级联仍持久写入停用。替换期间阻止 `Restore` 启动旧代码靠的是管理准入槽，不依赖持久标志，`TestRestoreCannotStartOldPackageDuringReplacement` 继续保护这一点。
- **命名空间重复配置**（`access/namespace.go` 的 `Configure`、`namespace_log.go` 的 `replay`）：按 `(Package, Name)` 匹配已有绑定。`DataVersion` 只作为新建命名空间的初始版本，已有命名空间保留当前版本。`QuotaBytes` 不同时更新配额并追加 `configure` 记录；新配额低于已用量时返回 Conflict 且不改变状态。replay 接受同包同名的 `configure` 记录作为配额更新。
- **YAML 符号**（`config/yaml.go`、`config/number.go` 的 `integerToken`）：只剥离一个前导符号，两处一致。
- **快照与安装同步**（`packagehost/archive.go` 的 `copyFile`）：删除逐文件 `Sync`。安装的持久性由提交点前的 `syncTree` 与目录同步保证；快照、Inspect、Discover、Pack、Sign 写入的都是临时目录，重开时清理。快照内签名证明改用普通写入。
- **选择响应取消**（`packagehost/store.go` 的 `prepareSelection`）：把调用方 ctx 传入 `inspectContent`。直接改为 `CheckSelect(ctx, ref, opt)`，不保留旧签名；Host.Replace 使用可取消入口。catalog 必须包含签名记录，删除从旧内容文件补读签名的回退。
- **nil Sink**（`access/diagnostics.go`、`host.go`）：改为不依赖接收者的包函数 `access.PublicError`，不保留旧方法；nil Sink 下 guest 调用失败的回归保护错误投影。

### 精简原则

- 同一不变式只在信任边界检查一次：wire 已校验的 initialize 帧预算与身份，SDK 与宿主门面不再重复；config 的 JSON/YAML/TOML 解析器在递归和分配前检查深度与集合数量，删除 `writeJSON` 及 Parse 结尾的重复检查。不能为集中校验而把恶意输入的拒绝延迟到建树之后。
- 不可能失败的函数不返回 error（`cloneCatalog`、`clonePackageChecked`、`Policy.Calls`），删除 `Endpoint.Close` 调用方不可能触发的分支。
- 无人读取的持久产物删除：`transaction.json` 写入、同步与删除及 catalog 的 transaction 字段。用户明确项目未发布，无需旧数据格式的兼容读取与迁移测试。
- 构造时拒绝 nil 依赖，方法内不再逐个检查 nil policy 或 nil ctx；未授权的 nil grant、公开零值/伪造 Grant 的拒绝仍是有效边界。`namespace_other.go` 为不支持平台提供显式失败实现，不是死代码，保留。
- recover 包装统一为一个辅助函数，统一以 `%w` 保留 error 类型的 panic 值。
- 测试：删除严格子集与重复测试；依赖 `ctx.Done()` 调用次数的白盒夹具改为黑盒加 `testing/synctest`；只断言错误文案片段的测试删除或改为断言可观察结果。

### Integrator 决策（2026-10-28）

用户授权以最小完整方案解决待决项，并明确项目尚未正式使用，不保留兼容性胶水代码；以下决定先记入规格，再实施。现有 v1 schema、签名字节、依赖版本均不变。

- **stderr**：限额只约束向宿主转发的总量，超限后继续 drain，不终止 activation。无需引入时间窗、速率状态或新开关。回调错误与进程退出后的 grace 约束保留。此项明确替代 `stabilize-cross-platform-conformance` 的“超限终止”旧规则。
- **发布者**：`Publishers[id]` 是唯一身份迁移判据；批准是完成一次迁移的授权，提交成功后当前发布者更新。删除遍历已安装版本的重复发布者检查，但保留同版本异摘要冲突、签名及撤销检查。向旧发布者迁回也需批准。
- **在途上限**：不在严格拒绝未知字段的 v1 initialize 中加字段。通用 Runtime 在启动前拒绝非零且非 64 的 `Inflight`；0 继续表示默认。custom SessionFactory 自行管理协议，不受此限制。低层 wire 可用于两端显式一致的非默认配置。选择显式配置失败而非静默覆盖或运行中误杀；未来确需跨语言动态协商时另作协议变更。
- **公开 API**：用户明确不需要兼容包装：仅保留包级 PublicError，CheckSelect 直接增加 ctx，删除未使用的 Store.Limits、OpenWithLimits、TaskManager.Lookup，Policy.Calls 去掉永远为 nil 的 error。Installed 保持确定性排列，不为排序额外制造新语义。`contract.Error.Is` 增加按 Code 匹配且保留标准 context 匹配；`UnknownFieldError` 提供字段名，config 用 errors.As 定位。`ConsumerDeclaration` 保留现有带完整路径的 metadata 检查，不新增会丢失 `FieldError` 定位的自定义解码器（独立审查已用回归确认）；通用进程层删除私有 `plugin.shutdown` 特判，custom Shutdown 已有 typed 映射，不猜测私有方法名。
- **清理并发**：移除全局 8 槽上限，每个 activation 本已有一次清理任务，不另加调度层。不承诺强杀进程内回调；Host.Close 仍等待本 Host 拥有的所有清理。
- **身份历史**：保留 `policy.seen` 全生命周期防复用；内存随累计激活身份线性增长。裁剪会使旧身份恢复准入，不能作为普通缓存逐出。文档明确 Policy/Host 的生命周期边界，不增加拍脑袋的阈值或拒绝服务开关。
- **Windows**：发现本机已运行且允许 agent 控制的 Windows 11 VM，计划用隔离源码副本实测关闭顺序及存储目录同步；测试结果仅对应被复制的代码，不冒充远程五平台 CI。
- **WASM watchdog**：独立审查实测指出 WithCloseOnContextDone 不只是检查取消，还提供 WASM 循环的调度检查点。保留它，仅删除无效 stdio AfterFunc；`GOMAXPROCS=1` 的无限循环在旧精简实现超过 5 秒被外部测试超时终止，恢复检查点后以 124 退出。不能仅凭 context 从未取消就删除这一运行时保障。
- **接收取消资源预算**：释放普通在途名额后，退出中的 handler 仍占资源。独立以 `len(incoming)-activeIncoming` 限制其数量为 `4*Inflight`，超过即关闭会话；复用已有取消预算尺度，不新增配置项，正常超时批次不占后续 health 名额。
- **迟到贡献**：移除重复 onRetire 后，外部 batch 与 builtin staged 最终写入均在 h.mu 内检查 activation context，沿用退役 hook 的同锁屏障，防止已清理的暂存记录被迟到 Start 重建。

## Alternatives

- 在 guest 端提高在途上限：只推迟问题出现的时机，拒绝。
- `Replace` 结束时补写 `Enabled=true`：中断时仍留下持久停用，拒绝。
- 命名空间 `Configure` 遇到版本差异时自动 `Migrate`：迁移须宿主显式确认，拒绝。
- 保留逐文件 fsync，只把快照移出 Store 锁：仍为丢弃的目录付出全盘刷写，拒绝。

## Validation

主要缺陷先写在旧实现上实际失败的回归，再修复；未复现的静态审查结论单独注明，不冒称红绿证据。精简项以既有测试保持通过为准，删除的测试须说明被哪个保留测试覆盖。每组完成后运行受影响包的 `go test -tags=integration -race`；全部完成后运行 `go vet -tags=integration ./...`、`go mod tidy -diff`、`bash conformance/run.sh`、`openspec validate --all --strict` 与文档检查，并由非作者 Review 各组增量。Windows 本地 VM 与远程 CI 分开记录。

## 审查证据（darwin/arm64，Go 1.27.3）

- 在途计数：临时探针 `TestProbeInflightAfterTimeoutBurst`（Inflight 4，4 个 50ms 超时调用后发 `health`）失败，输出 `session killed after timeout burst: read frame: EOF / guest=invalid_message: peer exceeded inflight limit`；探针已删除，工作树恢复干净。
- 替换启用意图：读码确认 `disableOne` 先 `SaveEntry(Enabled=false)`，`replace` 在 `store.Select` 返回非提交错误时直接返回，不重新启用。
- 命名空间：读码确认 `Configure` 以 `space.Config == config` 判等，`Migrate` 只改 `DataVersion`，replay 拒绝已存在命名空间的第二条 `configure` 记录；reviewer 以临时程序复现冲突。
- YAML：读码确认 `strings.TrimLeft(n.Value, "+-")` 剥离两个符号，`integerToken` 只剥离一个；reviewer 复现 `+-0x10` 得 16。
- fsync：读码确认 `copyFile` 每文件 `Sync`，`Snapshot` 经 `copyDirectory` 调用，安装后 `syncTree` 再同步一次。
- `deadcode -test ./...` 报告 `contract.BeginTask`、`Store.Discover`、`schema.Some`、`sdk.Serve`、`sdk.Main` 不可达，均为文档化公开 API，不视为死代码。
- `golangci-lint` 只报告 defer 中未检查的 Close 错误，未单列为问题。

## 实施与复核证据（2026-10-28）

- **真实红绿回归**：替换失败重开原先得到 disabled；迁移后的 Configure 与配额更新原先得到绑定冲突；YAML 双符号原先变为数字；Select decoder 取消后原先仍提交；stderr 超量原先导致健康调用 EOF；取消洪泛原先误杀会话。修复后上述回归均通过。
- **审查发现并修正**：恢复单 P WASM watchdog 必需的循环检查点；保留 Consumer 缺字段的 `FieldError` 完整路径；Windows 提交后失败注入改用跨平台都会失败的 NUL 路径；发布者刷新分支也更新权威记录；取消中的 incoming handler 加独立资源预算。原始 peer 回归在未加预算的实现上实际失败，修复后通过。
- **迟到贡献**：静态审查确认退役清理后的重新暂存窗口，两个最终写入临界区现检查 scope 取消。并发退役/暂存回归通过；未声称该回归已确定性重现原窗口，也未引入生产测试钩子或依赖 ctx 调用次数的夹具。
- **独立 Review**：access、packagehost、host/core、wire、runtime/SDK/config 及最终去兼容层各组均由非作者审查；修复了上述具体 findings。公开 API、签名记录必需、typed 自定义生命周期的最终审查未发现新增代码问题。
- **macOS arm64 / Go 1.27.3**：全仓 `go test -tags=integration -race ./... -count=1 -timeout=5m`、vet、tidy diff、完整 `bash conformance/run.sh` 均通过。最后一轮 wire/贡献退役重点测试另带 race 重复 20 次。config FuzzParse 10 秒及包路径、Manifest、ZIP fuzz 冒烟通过；这些不等于穷尽输入验证。
- **Windows 11 arm64 / Go 1.27.3**：最终隔离源码副本执行 `go test -tags=integration ./... -skip '^TestDocumentationTutorials$' -count=1 -timeout=5m` 和全仓 vet 均通过。此架构不支持 Go race；文档教程内部要求 race，故明确未运行该项，不将它记为完整 Windows Conformance。stdin 优先关闭、存储同步、worker 与真实 native/WASM 相关集成测试均包含在已运行测试中。
- **规格与文档**：`openspec validate --all --strict` 7 项通过；另两个既有 change 的归档 INFO 提示不属于本次新增失败。站点 24 页构建、89 文件链接/锚点检查、6 项 JS 测试通过。Python 验证 4 个 metaschema、70 个结构向量、1 个签名字节/hash 向量；代码级与验签行为由 Go conformance 运行。
- **未改动**：go.mod/go.sum、v1 JSON schema、签名字节和只读 third_party/cordis；未创建分支。未验证真实断电、全磁盘故障组合或不合作 builtin 的强制终止；框架不提供后者保证。

### 测试删改对应关系

- 根包的重复 Close/阻塞 Disable 测试由 `TestBlockedDisableAllowsUnrelatedAdmissionAndCloseCancellation` 保护；删除的 transaction.json 故障场景已无生产路径，CommitError 由 packagehost 根替换后的同步失败回归保护。
- wire 的 ctx 调用次数门控、私有 pending 篡改与 sleep 轮询改为真实帧/背压加 synctest；注册后立即请求、初始化取消和提交屏障仍保留。取消后健康调用与未确认取消的资源回归分别保护可用性和有界性。
- access 重复 Broker 撤销测试由 `TestPolicyGrantRevocationCancelsOnlyMatchingCalls`、`TestPolicyGrantRevocationDuringScopeCheckClosesAdmission` 覆盖两种调用入口；nil policy 在构造边界验证，未授权 nil grant 和无效 Grant 仍被拒绝。
- packagehost 的旧 catalog-only 模拟恢复测试由真实 `TestStoreRecoveryProcessExit` 覆盖；不再支持的 transaction/旧签名回退测试删除，不保留历史删除证明。FuzzManifest 仅检查不 panic，格式约束由专门的边界测试验证。
- SDK 初始化预算和身份拒绝的重复测试移至 wire 初始化边界；frame_budget 仍走真实启动，只改为断言错误分类而非文案。
