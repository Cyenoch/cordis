# Verification

## Local implementation

日期 2026-10-30，基线 `1d640b02074db9981a076c483e97999b5b11ec87` 之后的工作树；darwin/arm64、Go 1.27.3。最终实现 SHA 与五平台结果在推送后补记，基线不是本轮证据。

- 单包和 project 模式共用 devPlan、watcher 和候选生命周期。清单/配置按实际解析字节绑定 generation，显式资源强制监视，生成输出按精确路径排除。
- 所有包先构建、Pack、安装、按必需依赖选择并保存全部 entry 预检，再关闭旧组；关闭完成后再次复核输入。保留已有的取消进程树、过期候选丢弃与未完成清理目录保留。
- 两个独立 module 的 provider/consumer 示例实际跨包调用；测试验证修改生效、坏源码/配置/清单保留旧组、初始化失败与退出清理。

## Actual checks

- `go test -tags=integration -race ./cmd/cordis ./examples/pluginmeta -run 'TestDev|TestGeneratedDeclarations' -count=1 -timeout=4m` 通过。
- 最终 `bash conformance/run.sh` 通过全部 integration/race 及独立 native smoke。
- `bun run docs:examples` 通过，含 TestDev 系列；`bun run docs:test`、`bun run docs:build`、`bun run spec:validate` 通过。
- 同一最终工作树的 vet、module verify、schema 互操作、actionlint 与 diff 检查通过。

## Independent review

Linnaeus 发现解析计划与后续清单指纹可能不一致、临时文件名过滤会漏掉声明资源，已修正并加行为测试。Dirac 复核实现，发现保存 B 再撤回 A 的中间编译输入可能漏检；指纹已包含修改时间，`TestDevSourceRevertedDuringBuildInvalidatesCandidate` 保护这一行为。全部修正包含在最终本机 conformance 中。

轮询不能观察两次扫描间同时恢复内容与时间戳的外部改写；这不是源码冻结/HMR，文档明确该限制。

## Retirement ordering regression

实现提交 `969187e7ea880a3995fce2b167a8e4aa9d528e4f` 的[五平台 conformance](https://github.com/Cyenoch/cordis/actions/runs/41009346222) 全部通过，但独立[文档示例运行](https://github.com/Cyenoch/cordis/actions/runs/41009346201) 在联合开发退出时出现 `cleanup consumer: read frame: EOF`。因此该提交未打发布 tag。

`TestGuestRetirementNotifiesBeforeReturningReverseCall` 用 synctest 和通知同步点稳定复现：新的反向请求和在途请求都可能在 guest 接收退役通知前返回取消/拒绝，受管任务因此把正常退出当成错误。修复复用核心通知屏障，额外验证原请求主动取消仍能结束等待；不等待完整清理，不改变清理调用权限，不吞掉任务错误。

2026-10-30，darwin/arm64、Go 1.27.3，修复后的实际结果：

- 定向 race 生命周期与新回归测试通过。
- `TestDevProjectNativeGroupSwitching` 在 integration/race 下连续 5 次通过。
- `bash conformance/run.sh`、`bun run docs:examples` 全部通过。
- vet、module verify、docs:test、25 页 docs:build、严格 OpenSpec 和 diff 检查通过。

Socrates 独立审查通知顺序、engine 锁、请求取消、初始化与 cleanup 权限，未发现可证实缺陷；独立运行三个同步回归各 50 次及 core/wire/SDK race 测试，全部通过。同步回归模拟通知接收，真实进程覆盖由上述 native 集成承担。

## Pending delivery

五平台实际实现 SHA、CI 运行、归档和最终远端一致性尚待交付。
