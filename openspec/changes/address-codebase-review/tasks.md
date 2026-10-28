# Tasks

## 1. 审查

- [x] 1.1 对提交 164340f 做全仓只读审查，分包交给四个只读 reviewer，作者复核关键结论并以临时探针复现在途计数缺陷；结论记入设计的审查证据。

## 2. wire 写集（`wire/`）

- [x] 2.1 以超时洪泛后健康调用的回归确认旧实现失败，修复接收端只计有效在途请求；以原始 peer 回归确认取消后未退出的 handler 独立受四倍预算约束，超限关闭会话。
- [x] 2.2 取消监督由 `wg.Go` goroutine 改为 `time.AfterFunc`，删除 `cancelAck` 及其关闭逻辑，消除 Wait 之后 Add 的窗口。
- [x] 2.3 删除 guest 对自身注册输出的 `decodeRegistration` 自检；删除 `Endpoint.Close` 调用方不可能触发的错误分支；`frame.go` 的 ID 数字扫描交给 `strconv.ParseUint`；去掉读取 `ctx.Err()` 时多余的锁；`strings.CutPrefix` 替换前缀判断。
- [x] 2.4 测试：`lifecycle_test.go` 依赖 `ctx.Done()` 调用次数的夹具改为黑盒，保留 `handshake_budget_test.go` 中注册后立即请求的测试并改用 `testing/synctest`；`cancellation_test.go` 中轮询私有字段与 `time.Sleep` 的准入期限测试改为 synctest 并只断言 `Call` 返回时间。

## 3. 根包与 internal/core 写集

- [x] 3.1 以替换提交失败后重开的回归确认旧实现把 entry 持久写为停用，修复 `replace` 只退役不持久停用。
- [x] 3.2 合并 `executeCascade` 与 `disable` 的反向退役循环为一个辅助函数；合并三处“存储 entry 加 builtin 规格”快照构造。
- [x] 3.3 删除 `startExternal`、`startBuiltin` 中重复注册的 `onRetire`；外部 batch 与 builtin staged 写入在退役 hook 的同锁边界拒绝已取消 scope；删除 `Host.Status` 与 `Instances` 覆盖 `TemporaryStopped`、`Enabled` 的四行，并统一核心与宿主的定义及其测试。
- [x] 3.4 删除 `Engine.Fail`、`Engine.Enable`、`Engine.AddChild`，测试改用 `Add` 加 `Parent` 与 `Resume`；`Engine.Preflight` 与 `add` 共享准入检查，外部启用只校验一次配置。
- [x] 3.5 六处 recover 包装合并为一个辅助函数并统一以 `%w` 保留 error；`joinErrors` 改为 `errors.Join`；`entries` 映射改为具体类型并删除类型断言；`reserveEntry` 后重复的 `closed` 检查、四个延迟初始化的映射、`CreateChild` 重复的默认配置删除；未找到 entry 统一返回 NotReady 契约错误。
- [x] 3.6 补测试：级联卸载有活跃反向依赖实例；外部作者子实例的摘要校验、动态登记与父退役清理；Supervise 重启等待期间 Disable 后不再启动；Close 后 Enable、AddBuiltin、RegisterDefinition 返回 NotReady；nil Sink 下 guest 调用失败返回投影错误。
- [x] 3.7 删除 `host_lifecycle_test.go` 中 `TestCloseCancellationWhileDisableIsBlocked`（被 `management_test.go` 的阻塞停用测试覆盖）；`management_test.go` 的 `time.Sleep` 轮询改为 synctest。删除依赖已移除 transaction.json 的提交后失败注入，存储 CommitError 由 packagehost 提交边界回归保护。

## 4. packagehost 写集

- [x] 4.1 删除 `copyFile` 的逐文件 `Sync`，快照签名证明改为普通写入；以计数测试钩子或现有恢复集成测试确认安装中断语义不变。
- [x] 4.2 以取消回归确认旧实现丢失取消，把调用方 ctx 传入 `prepareSelection` 与内容哈希；回归让 decoder 取消调用方后仍返回有效 metadata，由后续内容校验拒绝取消。
- [x] 4.3 删除 `transaction.json` 全部逻辑与 `catalog.Transaction`；按用户明确的未发布前提，不保留 legacy 解码字段及迁移测试；同步 `docs/recovery.md`。
- [x] 4.4 `cloneCatalog`、`clonePackageChecked` 不再返回 error，改用 `maps.Clone`、`slices.Clone`，保持 `Signatures` 非 nil。
- [x] 4.5 删除 `checkFileNames` 与调用方重复的 `validatePath`，在 `checkDirectories` 加精确重名检查；合并 `store.go` 与 `discover.go` 中重复的开发目录暂存；`equalBytes`、`inUse`、`syncDirectory` 换为 `bytes.Equal`、`slices.Contains`、`syncDir`；`sort` 换为 `slices`；删除 `author.go` 中无效的 `json.Compact`；`inspect` 的可变解码器参数改为普通参数；Windows `syncDir` 在无 catalog 时返回，保留根 catalog 同步。macOS 与 Windows integration 验证通过。
- [x] 4.6 补测试：`Uninstall` 带 `DeleteData` 成功后数据目录删除、数据版本下限清除、新发布者可接管该 ID。
- [x] 4.7 `FuzzManifest` 只保留不 panic 断言；删除被进程级恢复测试覆盖的 `TestStoreCrashRecoveryCatalogIsOnlyCommitPoint`。

## 5. access 写集

- [x] 5.1 以迁移后重开配置与配额调整的回归确认旧实现冲突，修复命名空间 `Configure` 与 replay；同步 `docs/access.md` 与 `docs/access-tutorial.md`。
- [x] 5.2 补测试：`Policy.Revoke` 与同 entry 新激活替换都取消在途租约；迁移后压缩并重开保留数据版本；诊断压缩在最大标签长度下不超过 `MaxBytes` 并删除不可达的超限分支。
- [x] 5.3 删除与 `policy_test.go` 重复的 `broker_test.go` 撤销测试；构造函数拒绝 nil policy，删除契约内重复 nil 检查，保留未授权 nil grant 与公开无效 Grant 拒绝；保留有实际平台失败实现的 `namespace_other.go`；键排序改用 slices/maps/cmp。

## 6. config、schema 与 cmd/cordis 写集

- [x] 6.1 以 `+-0x10`、`-+0x10`、`+-0o7` 的回归确认旧实现，修复 YAML 只剥离一个符号。
- [x] 6.2 保留各解析器建树前的深度与数量边界，删除 `config.Parse` 末尾 `CheckJSON` 自检和 `writeJSON` 重复检查；`config/value.go` 键排序改用 `slices`。原计划把校验推迟到 writeJSON 会失去建树前的资源限制，已调整设计。
- [x] 6.3 删除 `cmd/cordis/generate.go` 对 `GenerateMany` 已格式化输出的再次 gofmt。

## 7. runtime、cmd/cordis-worker 与 sdk 写集

- [x] 7.1 删除 worker 中永远不会触发的 stdio 关闭 `AfterFunc`；保留 WithCloseOnContextDone 的 WASM 循环检查点，避免单核环境 startup watchdog 无法调度，新增真实进程红绿回归。
- [x] 7.2 删除 SDK 对 wire 已保证的 initialize 帧预算与身份的重复校验及其不可达测试；`native.Start` 删除与 `process.Start` 重复的 ctx 检查；`slices.Clone`、`slices.Sort`、`slices.Chunk` 替换手写代码。
- [x] 7.3 `frame_budget_test.go` 保留真实启动边界并改为断言契约错误而非文案；`sdk/capabilities_test.go` 只保留宣告能力均可协商的断言；`sdk` 中的时序测试改为 synctest。
- [x] 7.4 在 Windows 上验证宿主 transport 关闭顺序，确认后改为先关闭 stdin 写端再关闭 stdout 读端；Windows 11 arm64 实机 integration 与 vet 通过，race 不受该架构支持，五平台远程结果另记。

## 8. Integrator 待决项

- [x] 8.1 stderr 限额只限制转发，超限继续 drain 而不终止；规格已更新，完成 native/WASM 行为回归与文档。
- [x] 8.2 发布者批准用于一次迁移，以保留发布者为准，删除已安装版本的重复身份检查；普通安装与同 digest 刷新均更新候选 catalog，完成迁回旧发布者、撤批后升级回归及文档。
- [x] 8.3 不改 v1 initialize；通用 Runtime 启动前拒绝与 SDK 不一致的在途上限，custom 协议不受影响，完成行为回归与协议文档。
- [x] 8.4 不留兼容胶水：PublicError 只留包函数，CheckSelect 直接增加 ctx，删除无调用的 Limits/OpenWithLimits/Lookup，Calls 去掉 error；通用进程层删除 plugin.shutdown 私有方法名特判，custom 生命周期仍走 typed 映射；增加 Code 匹配与类型化未知字段，保留 ConsumerDeclaration 结构化路径，完成独立审查。
- [x] 8.5 移除 `cleanupSlots` 全局上限并加无关实例清理回归；保留 `policy.seen` 防复用，文档说明线性增长与生命周期边界。

## 9. 收尾验证

- [x] 9.1 运行 `go vet -tags=integration ./...`、`go mod tidy -diff`、`go test -tags=integration -race -count=1 ./...`、`bash conformance/run.sh`、`openspec validate --all --strict` 与文档检查，由非作者 Review 各组增量；平台、测试替换映射及未验证边界见 design.md。
- [ ] 9.2 推送后核对五个远程平台的 Conformance 结果。
