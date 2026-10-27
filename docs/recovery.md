# Package Store 恢复指南

## 唯一提交依据

`catalog.json` 同时记录 installed、selected、entry 和宿主确认的数据版本下界；原子替换此文件是提交点。`transaction.json` 是操作记录，`catalog.next` 是候选根，二者都不是恢复时的提交依据。不能因为日志中出现了 v2，或 `content/` 已经存在 v2，就将它推断为已安装或已选择。候选根在替换前必须经过与 Open 相同的严格解码及内容校验；catalog 单独预留三层封装深度，manifest 和配置仍分别保持深度 32 的限制，不能因嵌套合法叶文档而写入不可恢复根。

`Open` 取得存储独占锁后，先读取并校验 `catalog.json`，再清理 staging、旧 activation snapshots、未被 catalog 引用的 content 以及事务残留。catalog 引用的内容目录缺失时返回错误，不通过日志或候选根拼接、猜测安装状态。宿主应保留故障存储用于诊断，从可信包或宿主备份显式恢复，不手工把 `catalog.next` 改名冒充已提交事务。

仅安装 v2 不改变 v1 的选择，也不启动 v2。重启后宿主必须重新创建 activation 身份，取得新的 `Snapshot` 再启动；enabled entry 是持久期望状态，不表示崩溃前进程仍在运行。显式 `Select(v2)` 才改变选择；停止旧 activation、依赖预检、业务数据迁移确认与旧代码恢复的限制见 [包管理文档](packages.md)。

## 中断边界

提交顺序及恢复预期如下：

| 中断位置 | 安装 v2 后重开 | 显式选择 v2 后重开 |
| --- | --- | --- |
| `transaction.json` 文件与 root 目录已同步 | 仅 v1 已安装且选择 v1；删除未移动 staging | v1/v2 均已安装，仍选择 v1 |
| staging 已 rename 到 content，尚未同步内容树 | v2 仍未提交，删除孤立 content，选择 v1 | 不适用；选择不移动内容 |
| `catalog.next` 文件已同步，尚未原子替换 | v2 仍未提交，删除候选根及孤立 content，选择 v1 | 保留原 catalog，仍选择 v1 |
| catalog 已原子替换，尚未同步 root 目录 | 进程退出后可见新 catalog，v2 已安装但选择 v1 | 进程退出后可见新 catalog，选择 v2 |
| root 目录已同步，事务记录尚未清理 | v2 已安装但选择 v1；删除日志残留 | 选择 v2；删除日志残留 |

原子替换后、root 同步前的测试模拟的是**进程退出**，不是机器断电或文件系统掉盘。操作系统仍存活时新 catalog 可见；真正掉电后的持久性取决于文件系统和目录同步语义，不能从本测试宣称所有硬件/文件系统均已验收。若替换后同步或清理返回错误，API 用 `*CommitError` 明确表示已过提交点；调用者不能按“完全没有发生”盲目重试。

取消也不等于回滚：一般安装/选择在提交前观察到 context 取消，返回 `context.Canceled` 且不提交该操作；提交后取消不伪报未提交，安装仍返回实际包，选择仍返回成功。**宿主已经提交的数据迁移不是可取消的选择候选**：宿主必须在迁移完成后立即调用 `ConfirmDataVersion(ctx, id, version)`，在任何版本预检、选择或执行之前独立持久化数据版本下界。此 API 有意忽略取消，不要求目标代码已安装或通过信任检查，只允许递增或相同版本幂等确认。兼容调用 `Select(..., ChangeOptions{MigrationCommitted: true, DataVersion: version})` 也先独立记录事实，即使随后的目标检查、依赖/配置验证、InUse 或取消导致选择失败，旧代码的 Snapshot 仍必须被拒绝，重开亦然。只读 `CheckSelect` 不记录该事实，Facade 替换流程也必须先确认再预检。

迁移确认失败时不得继续版本操作或恢复旧代码；宿主应修复持久化问题并核对实际状态。`*CommitError` 表示该确认已经替换 catalog、后续同步或清理失败；通过 `errors.As` 识别被 Select 包装的确认错误，此时新代码选择尚未发生。选择失败不会自动回滚 selection：原引用可能仍显示 selected，但因持久数据下界而不可执行。业务数据库、网络副作用与迁移补偿由宿主管理，Store 不做自动业务回滚。

## 真实进程退出验收源码

`packagehost/recovery_integration_test.go` 使用 `integration` build tag。父进程建立真实签名 ZIP、安装并选择 v1、保存 enabled entry，然后启动当前测试二进制中的子测试。子进程先创建 v1 的真实 snapshot，再在包内测试设置的 `Store.commitObserver` 回调中调用 `os.Exit(86)`，绕过 Store/Snapshot 的所有 defer 清理。生产 Store 中该未导出字段为 nil，没有环境变量注入、公开故障 API 或模拟提交实现；环境变量仅由 `_test.go` 子测试读取，用来传递测试配置路径。

`TestStoreRecoveryProcessExit` 覆盖五个安装中断位置，以及四个适用的选择中断位置。父进程要求子进程返回指定退出码，先直接读取 `catalog.json` 检查提交前后 revision、安装集合及选择；不使用日志判断提交。重开真实 Store 后检查：

- 安装 v2 不改变 v1 选择，已提交 v2 仍不允许取得执行 snapshot；选择事务在提交点前后分别保留 v1 / 选择 v2。
- installed 与 enabled entry 保持一致，已安装内容的摘要、完整性与签名可重新校验。
- 进程锁可重新取得，未提交 staging、孤立 content、旧 snapshot、候选根和事务残留均无泄漏。

`TestStoreRecoveryCancellationBoundary` 在 journal 同步后及 catalog 原子替换后同步取消 context，分别约束安装与选择的未提交取消/已提交成功语义，并重开检查结果。

`TestStoreNestedDocumentsRemainReopenable` 用真实签名包保存达到叶文档深度上限的 manifest 和 entry 配置，重开并取得 Snapshot，同时断言超限配置被拒绝。`TestStoreCandidateValidationPreservesCommittedRoot` 约束候选根校验失败不得损坏已提交根。`TestStoreCommittedMigrationSurvivesFailedSelection` 覆盖预先取消、选择提交前取消、信任拒绝、InUse、迁移版本不匹配和目标缺失：确认不改变旧选择，但立即及重开后的旧 Snapshot 均因数据版本被拒绝。`TestStoreConfirmDataVersionIsIndependentAndMonotonic` 覆盖无已安装代码时取消仍确认、同版本幂等和拒绝降低下界。`TestStoreConfirmDataVersionCommitErrorRetainsFact` 用确定性的日志清理失败约束 `CommitError`，确认错误不会丢失进程内或重开后的数据下界。

集成者执行：

```sh
go test -tags=integration ./packagehost -run '^TestStoreRecovery' -count=1 -timeout=2m
go test -tags=integration -race ./packagehost -timeout=5m
```

集成负责人已执行的恢复、集成和 race 结果见 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)。上述命令用于复现，不是独立新增的通过记录；提交点、进程锁和目录同步必须按实际 OS/架构及对应源码修订判断，交叉编译不代表平台运行验收。
