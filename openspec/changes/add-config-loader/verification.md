# Verification


## 场景与测试

`contract`、`config` 的测试在各自的包里，其余都在 `loader` 包。

| 需求 | 场景 | 测试 |
|---|---|---|
| 显式注册表与名称解析 | 未知插件名 | `TestUnknownPluginLeavesOtherEntriesAndTheFileAlone` |
| | 重复登记 | `TestDuplicateRegistration` |
| 配置文件到实例树 | 分组的子树 | `TestGroupChildrenStopBeforeTheGroupIsRemoved` |
| | 匿名条目保持不变 | `TestReloadOnlyTouchesWhatChanged` |
| 配置解码与错误定位 | 默认值与覆盖 | `TestDefaultsAreDecodedOntoAFreshValuePerEntry` |
| | 默认值不被条目共享 | `TestDefaultsAreDecodedOntoAFreshValuePerEntry` |
| | 未知字段的位置 | `TestUnknownConfigKeyIsLocated`；同名字段各自定位见 `config` 包的 `TestDecodeLocatesFailuresByPath` |
| 条目的依赖、设置与隔离 | 条目级依赖 | `TestEntryRequirementsGateThePlugin` |
| | 带标签的共享隔离 | `TestIsolationLabelsAndSettings` |
| | 未登记的设置 | `TestIsolationLabelsAndSettings` |
| 禁用 | 禁用与启用 | `TestDisableAndEnable` |
| | 禁用分组 | `TestDisabledGroupKeepsItsChildrenUnmarked` |
| 运行期编辑 | 只改配置 | `TestConfigOnlyUpdateIsAppliedInPlace` |
| | 改变隔离 | `TestChangedIsolationReloadsTheSubtree` |
| | 非法编辑 | `TestInvalidEditChangesNothing` |
| | 编辑不等待稳定 | `TestEditsReturnBeforeTheInstanceSettles` |
| | 并发改名 | `TestConcurrentPluginChangesConverge` |
| 写回 | 保留注释 | `TestWriteBackKeepsCommentsAndOrder`（逐字节对照） |
| | 写回前文件已被修改 | `TestExternalChangeIsMergedBeforeWriting` |
| | 写回被取消 | `TestCanceledWriteKeepsTheEditPending` |
| | 只读格式 | `TestReadOnlyFormat` |
| | 读取不写回 | `TestUnknownPluginLeavesOtherEntriesAndTheFileAlone` |
| 重新读取与冲突 | 只影响变化的条目 | `TestReloadOnlyTouchesWhatChanged`、`TestReloadAddsAndRemovesEntries` |
| | 损坏的文件 | `TestBrokenFileIsNeitherAppliedNorOverwritten` |
| | 同键冲突 | `TestFileWinsOverAnUnwrittenEditOfTheSameKey` |
| 嵌套配置文件 | 子树与复合标识 | `TestIncludedFileHasItsOwnIdentifiersAndWriteBack` |
| | 初始内容 | `TestInitialContentCreatesAMissingFile` |
| | 不可解析的被引用文件 | `TestUnreadableIncludedFileIsNotReplacedByInitialContent` |
| 嵌套文件的补丁 | 补丁覆盖配置 | `TestPatchesReplaceKeysAndInsertEntries` |
| | 归属决定写回位置 | `TestEditsAreWrittenToWhereTheKeyComesFrom`、`TestLaterPatchOwnsTheKeysItOverridesOnAnInsertedEntry` |
| | 无法应用的补丁 | `TestPatchesReplaceKeysAndInsertEntries` |
| 就绪等待 | 空闲后启动 | `TestReadyGatesTheStartOnly` |
| | 之后的编辑不影响已启动者 | `TestReadyGatesTheStartOnly` |
| | 缺依赖的条目不阻塞 | `TestReadyGatesTheStartOnly` |
| native 包条目 | 配置文件中的 native 包；在用的包不能卸载；未选中的包 | `hosting` 包的 `TestLoaderRunsNativePackages`（内存运行时上的真实 SDK）。“替换”与“卸载”走同一个在用集合，只测了卸载；“未选中”用的是没有选中版本的包标识，错误与 `Host.Enable` 的相同 |
| 状态查询与关闭 | 插件查询自己的条目 | `TestPluginFindsAndDisablesItsOwnEntry` |
| | 变更事件 | `TestChangedReportsEditsAndReloads` |
| | 停止时写出 | `TestPendingEditsAreWrittenOnStop` |

另有：`TestMoveWithinAGroupDoesNotRestart`、`TestFailedEntryRecoversThroughAConfigUpdate`、`TestPatchesSurviveAReloadOfTheIncludedFile`、`TestWatchReloadsAChangedFile`，以及 `loader` 的 `Example` 与 `examples/loader` 的 `Example_run`（输出即 `docs/loader.md` 引用的内容）。

## 官方测试的归类

对照 `third_party/cordis/packages/{loader,include}/tests`，提交 `35fdc96`。`group` 包没有独立的测试目录，用例在 `loader/tests/group.spec.ts`。

| 文件 | 用例 | 归类 |
|---|---|---|
| loader/index | basic support：initiate、update | 移植：`TestUnknownPluginLeavesOtherEntriesAndTheFileAlone`、`TestConfigOnlyUpdateIsAppliedInPlace` |
| | plugin self-update、self-dispose | 有意差异：没有“实例更新或停止自己即写回”的约定，用 `EntryOf` 加 `Update` 显式表达，`TestPluginFindsAndDisablesItsOwnEntry` |
| | intercept config：pending、resolved | 移植为条目的 `settings`：`TestIsolationLabelsAndSettings` |
| | entry failure：on load、on config update、分隔符标识 | 移植：`TestUnknownConfigKeyIsLocated`；含 `:` 的标识由文件校验拒绝 |
| | failed entry recovery、pending entry update | 移植：`TestFailedEntryRecoversThroughAConfigUpdate` |
| | parent teardown | 移植：`TestEntryRequirementsGateThePlugin`（依赖撤销后条目等待而不是被禁用） |
| | disabled js expression、config interpolation、nested plugin config | 不适用：没有表达式求值 |
| loader/group | basic support：disable/enable inner、outer | 移植：`TestDisableAndEnable`、`TestDisabledGroupKeepsItsChildrenUnmarked` |
| | transfer | 有意差异：移到另一个组会重新加载；同组内移动见 `TestMoveWithinAGroupDoesNotRestart` |
| | awaited reconciliation | 移植：`TestEditsReturnBeforeTheInstanceSettles`（用 `Wait` 等待） |
| | intercept | 移植为分组的 `settings`：设置作用于后代，由内核的作用域派生保证 |
| loader/isolate | basic、realm | 移植：`TestIsolationLabelsAndSettings`、`TestChangedIsolationReloadsTheSubtree` |
| | transfer、migration；isolate-provide 全部 | 有意差异：隔离变化时重新加载条目，不就地迁移 |
| loader/commit | create、update、move、remove 的变更报告；读取不报告 | 移植：`TestChangedReportsEditsAndReloads` |
| | fiber.update 报告、子 fiber 自行停止 | 不适用：没有实例到条目的隐式回写 |
| | file-driven moves、EntryTree disposal | 移植：`TestReloadAddsAndRemovesEntries`、`TestPendingEditsAreWrittenOnStop` |
| loader/validate | 合法与非法选项、运行期拒绝、非法后恢复 | 移植：`TestInvalidEditChangesNothing`、`TestFailedEntryRecoversThroughAConfigUpdate` |
| | 表达式只允许用于 disabled | 不适用 |
| loader/rebuild | inject 变化时重建、重建期间被移除、合并两次更新 | 移植：`TestEntryRequirementsGateThePlugin`、`TestConcurrentPluginChangesConverge` |
| loader/evaluate | 全部 | 不适用：围绕表达式求值上下文；条目级依赖的等待见 `TestEntryRequirementsGateThePlugin` |
| loader/import、resolve、internal | 全部 | 不适用：Node 的模块解析；这里是显式注册表与 `Resolver` 接口 |
| include/patch | 不带补丁、覆盖 config、禁用、名称不符、标识不存在、插入根与分组、插入到非分组、多个补丁、后面的补丁指向前面插入的条目 | 移植：`TestPatchesReplaceKeysAndInsertEntries`、`TestLaterPatchOwnsTheKeysItOverridesOnAnInsertedEntry`、`TestPatchesSurviveAReloadOfTheIncludedFile` |
| include/location | 校验问题、分组内条目、补丁中的值、JSON 的位置 | 移植：`TestUnknownConfigKeyIsLocated`、`TestIsolationLabelsAndSettings`、`TestPatchesReplaceKeysAndInsertEntries`（未应用补丁的位置在引用方文件） |
| | 别名、表达式、插件导入的位置 | 不适用：YAML 别名经 `config` 解析后展开；没有表达式和模块导入 |
| include/write | 重命名的瞬时失败重试 | 有意差异：不做自动重试，失败的写入留到下次写入或停止，`TestPendingEditsAreWrittenOnStop` |
| | 不覆盖等待期间落盘的改动 | 移植：`TestExternalChangeIsMergedBeforeWriting` |
| include/sync | 合并不同条目的文件编辑与运行期编辑、同键冲突、不覆盖无法解析的文件 | 移植：`TestExternalChangeIsMergedBeforeWriting`、`TestFileWinsOverAnUnwrittenEditOfTheSameKey`、`TestBrokenFileIsNeitherAppliedNorOverwritten` |
| | 文件删除或禁用条目时不写回 | 移植：`TestReloadAddsAndRemovesEntries` 之后文件保持测试写入的内容 |
| | 补丁归属的写回、插入条目的写回 | 移植：`TestEditsAreWrittenToWhereTheKeyComesFrom` |
| | 向补丁插入的分组里增删条目 | 有意差异：返回 `ErrPatched`，由使用者改引用方文件 |
| | 匿名条目跨重新读取保持稳定 | 移植：`TestReloadOnlyTouchesWhatChanged` |
| | 只读文件上的运行期编辑留在内存 | 有意差异：只读格式的编辑被拒绝且不生效，`TestReadOnlyFormat` |
| | 复合标识移除、停止时重试与写出 | 移植：`TestPatchesSurviveAReloadOfTheIncludedFile`、`TestPendingEditsAreWrittenOnStop` |
| include/reload | 每次重新读取都重新应用补丁、补丁移除后还原、无效文件后仍可重新读取、不以 initial 覆盖 | 移植：`TestPatchesSurviveAReloadOfTheIncludedFile`、`TestBrokenFileIsNeitherAppliedNorOverwritten`、`TestUnreadableIncludedFileIsNotReplacedByInitialContent` |
| include/parse、path | YAML 版本与标签、Windows 路径到 URL | 不适用：解析复用 `config` 包；路径用 `filepath` |

## 独立审查（任务 9.2）

由非作者 Agent 分两轮审查 `loader`，每一项都有复现。全部处理，回归测试在 `loader/review_test.go`。

第一轮（7 个阻塞项、9 个应修项）。问题集中在写回与合并：失败后重试、路径和移动的下标在内存与文件之间不一致。当前做法：不保存编辑日志，每个条目记着文件里的版本，写回时在重新解析的 YAML 上按内存的树同步，写入前把结果解析回来与内存比较。

| 问题 | 测试 |
|---|---|
| 写入失败或取消后重试，移除、创建、移动被重复应用或丢失 | `TestRetriedWriteAppliesEachEditOnce` |
| 文件把条目移入或移出分组时 panic 或丢失条目 | `TestFileMovesAnEntryBetweenGroups` |
| 被移除分组的后代仍可被编辑，编辑写进了别的条目 | `TestRemovedGroupTakesItsChildrenAlong` |
| 未写出的移除在重新读取后被文件里的同名条目复活 | `TestUnwrittenRemovalSurvivesAReload`、`TestFileChangeCancelsAnUnwrittenRemoval` |
| `Move` 的下标把不在文件里的兄弟条目算进去 | `TestMoveCountsTheEntriesTheFileHas` |
| 文件删掉分组后，其下未写出的创建与编辑使写回永久失败，且不报告 | `TestUnwrittenEditsBelowWhatTheFileRemovesAreReported` |
| 清空补丁提供的键写出 `null` | `TestClearingAKeyThatAPatchProvides` |
| 带条目级依赖的条目首次加载失败后不恢复；provider 重启后句柄过期 | `TestEntryWithRequirementsRecoversAndFollowsItsProvider` |
| 文件定义了未写出条目所用的标识后，该条目查不到 | `TestFileTakesOverAnIdentifierOfAnUnwrittenEntry` |
| 文件删除后又加回一个仍在停止的条目，条目保持被移除 | `TestFileReaddsAnEntryThatStillStops` |
| `Reload` 不重试加载失败的嵌套文件 | `TestReloadRetriesAnIncludeThatFailed` |
| `~1` 这样的标识与运行期标识冲突 | `TestReservedIdentifiersAreRejected` |
| 没有变化的编辑仍发事件；快照不含配置；零值注册表 panic；监视循环的数据竞争 | `TestEditsThatChangeNothing`、`TestZeroRegistry`、`TestWatchReloadsAChangedFile`（`-race`） |

第二轮针对重写后的合并与写回（1 个阻塞项、6 个应修项）：

| 问题 | 测试 |
|---|---|
| 未写出地移入某分组，文件随后删掉或替换该分组，条目从内存和文件中一起消失 | `TestEntryMovedIntoAGroupTheFileRemoves`、`TestEntryMovedIntoAnUnwrittenGroupTheFileReplaces`、`TestRevivedEntryLeavesTheRemovedGroupItWasMovedInto` |
| 文件把分组改成插件后，其下未写出的创建留下占位标识 | `TestUnwrittenChildOfWhatIsNoLongerAGroup` |
| 未写出的移除挤占了未写出的移动的位置 | `TestUnwrittenRemovalDoesNotDisturbAnUnwrittenMove` |
| `plugins:` 没有值、标量带行内注释再改成列表时无法写回 | `TestWriteBackIntoUnusualFiles` |
| `-0`、控制字符、转义的非 BMP 字符等值导致写回报错或误报 | `TestValuesYAMLMustCarry`（YAML 承载不了的值在生效前被拒绝） |
| 重新读取或编辑之后补丁状态不更新，与重新加载同样的文件得到的树不一致 | `TestPatchesFollowTheTree` |

审查中确认无问题的部分（审查者实际运行）：四个 goroutine 随机编辑、重新读取并伴随外部改写与写入失败的压力测试在 `-race` 下 20 轮无竞争、无死锁，结束后内存与重新加载一致；CRLF、无结尾换行、`---`、流式写法、带标签与块标量的文件在无关编辑后保持原样。未覆盖：嵌套文件上的压力测试、Windows。

本机：`go vet ./loader` 无输出，`go test -race -count=30 ./loader` 通过。

## 与规格、任务的出入

- 规格“状态查询与关闭”要求在停止完成前写出未写出的变更。条目是加载器的子实例，内核先停止它们，写出发生在加载器自己的清理里；两者先后不影响文件内容。
- 任务 7.1 实现为方法 `(*hosting.Host).Resolver()`，条目写作 `package:<包标识>` 或 `package:<包标识>#<定义>`。这类条目在宿主的实例枚举里标识为 `loader/<条目标识>`、来源 `dynamic`。

## CI 五平台

提交 `5c1aeb1`（`main`）：

- Conformance：https://github.com/Cyenoch/cordis/actions/runs/43013279520 。`go vet -tags=integration ./...`、`go mod verify` 与完整验收脚本（`conformance/run.sh`，Windows 为 `conformance/run.ps1`）在 ubuntu-24.04（x64）、ubuntu-24.04-arm、macos-15（arm64）、macos-15-intel、windows-2025 上全部通过；契约校验作业（`validate-schemas.py` 与 `spec:validate`）通过。
- Documentation：https://github.com/Cyenoch/cordis/actions/runs/43013279400 ，通过。

此前两轮（`1a42ce3`、`78c031f`）在 CI 上暴露并已修复的问题：应答发出后对端可在本端释放在途名额之前发来下一个请求，被判越限（`wire`，写出端空闲时先释放名额再交出应答）；`logging` 的测试在 Windows 上没有先关闭临时文件；两个测试依赖时序（链并发测试要求至少一次成功、流测试用固定时长等待）。
