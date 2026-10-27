# Tasks

本清单记录实施进度；仅在对应行为经实际验证后勾选，临时诊断不是修复验收。先于 `negotiate-session-frame-budgets` 实施，共享 wire 和并行生命周期控制由 Integrator 串行集成。

## 1. Wire 响应隔离与协议说明

写集：`wire/`、`docs/protocol.md`；owner 为 Integrator / wire。

- [x] 1.1 把双向超大成功结果、超大 coded error 和无关在途调用的失败复现转为真实 endpoint 行为测试；记录修复前失败，验证无原响应字节写出、后续调用成功且 activation 不变。
- [x] 1.2 在唯一编码路径增加本地尺寸分类，普通响应至多发送一次固定 `plugin_failed` 小错误；验证实际 JSON+LF 的 N/N+1、转义膨胀和同关联错误，不用 message/code 猜测本地失败阶段。
- [x] 1.3 复用原取消准入与监督处理编码期间取消、迟到错误；验证取消/超时不被尺寸错误或迟到成功覆盖，正常确认不误关会话，未确认取消仍有界失败。
- [x] 1.4 保留 initialize、health、shutdown、activation.publish、activation.retire 和初始化前反向响应的生命周期边界；验证超限初始化不发布、shutdown/retire 不伪造成功，也不重放已执行副作用；断链 disposer 失败必须可观察，不能断言远端释放成功。
- [x] 1.5 覆盖小错误也装不下、无效 JSON、部分写入报错、零进展写入、发送准入背压和实际收到违规帧；验证显式会话失败且不递归替代；本地大请求另验仅调用失败且远端未执行。
- [x] 1.6 同步协议文档的完整帧计量、错误码/安全固定说明、控制与业务边界和预算尚待统一限制；运行 `go test -race ./wire`，核对文档未把入队成功写成对端已收到。

## 2. SDK 与真实运行时回归、作者指南

写集：SDK tests、Runtime integration fixtures、独立作者/Host 验收、`docs/plugin-author.md`、`docs/troubleshooting.md`；不重复改 wire 或 contract。

- [x] 2.1 使用真实 SDK ServeTransport 覆盖超大结果与错误，确认进程入口不退出、后续正常请求成功；保留实例内业务串行，不为测试绕过并发契约，运行对应 SDK 测试。
- [x] 2.2 扩充独立 Native/WASI 作者包验收：执行一次有计数的调用后让结果超限，再读回计数与 activation；验证不重启、不重试、不重复副作用，健康邻居不受影响。
- [x] 2.3 更新单记录超限、转义、分页/投影选择和“错误不代表回滚”的作者示例与排障说明；按示例运行真实构建/调用，保留并行文档元数据并记录实际平台。

## 3. 集成审查与验收记录

写集：本 change 与最终集成记录；不把归档当作实施完成任务。

- [x] 3.1 非作者审查尺寸分类、取消竞态、控制方法及业务副作用语义，确认没有旧行为开关、payload 泄露或跨层第二套发送器；在本 change 记录实际发现和处置。
- [x] 3.2 运行相关 race 测试、`bash conformance/run.sh`（实际 Windows 验证使用 `conformance/run.ps1`）及当前 docs 校验，记录成功、跳过和不可运行范围；严格验证本 change 后才勾选已完成任务。

## Workflow follow-up

- 整体预算交付继续实施 `negotiate-session-frame-budgets`，不把本步骤单独描述为已经支持不同宿主预算。
- 经用户要求归档时先建立 build 主规格基线，再应用本 delta，保留并行新增场景。

## Implementation Evidence

2026-10-27，darwin/arm64，Go 1.27.3。仅记录当前检出实际执行，代码未提交，文档保留原 codeCommit 与差异提示。

- `TestOversizedResponseIsolatesCall` 在修复前四个双向 result/error 场景均因 EOF 失败；修复后与 wire 全部测试以 `-race -count=3` 通过。记录最终编码 JSON+LF 边界、转义、固定小错误唯一写出及 activation 不变。
- `go test -race ./wire ./sdk ./contract ./runtime/internal/process -count=1` 通过；覆盖本地大请求不执行远端、迟到取消不覆盖、控制响应/坏 JSON/零写入/部分写入继续 fatal。
- `go test -tags=integration -race ./runtime ./sdk -count=1 -timeout=3m` 通过；独立临时 module 构建真实 Native/WASI，4096/16384 预算下验证大结果/大错误、宿主反向结果、业务计数只执行一次、activation/PID 不变及邻居存活。
- `bun run docs:test`、`bun run docs:sync`、`bun run docs:build` 通过；24 页、88 文件的链接/锚点/资源校验通过，sync 对未提交关联代码保留告警。尚未部署。
- Windows/Linux 实机未运行；WASI 插件在本机 worker 内实际执行，不等于 Windows/Linux 平台验收。
- 独立非作者审查确认 wire 绑定、确认提交屏障及响应隔离未发现其他明确问题，发现 process 拒绝工厂时同步 Close 阻挡 kill/reap、Host 未拒绝 generic Runtime 的 custom 映射两项，已修复并补真实 Native 阻塞读取关闭与 Host 负向回归；定向 race 通过。该审查不是 Windows 实机证据。
- 首轮 `bash conformance/run.sh` 已实际执行但失败：并行 build change 的 packagehost/CLI fixture 尚缺 definition configSchema、管理恢复测试传入 null；目标 wire/SDK/Runtime、文档教程与其余已输出包通过。等待相关 owner 同步后重跑，不将整仓门禁标为完成。
- 上述并行 fixture 及退役 cleanup 调用链由其 owner 修复后，18:29–18:31 的 `bash conformance/run.sh` 退出 0：全仓 integration/race、独立 basic-host Native/WASI 与 lifecycle-smoke Native/WASI 均通过。不是把前轮失败改记为成功，也不代表后续并行树已验收。
- 最终 `bun run docs:test`、`bun run docs:sync`、`bun run docs:examples`、`bun run docs:build`、`bun run spec:validate` 和 `git diff --check` 通过；源码未提交，docs 的 codeCommit 差异告警仍保留，不伪造已发布基线。
- 后续测试增强后的全量重跑按用户要求主动中断，退出 130，日志 `/tmp/cordis-budget-conformance-interrupted.log`；不把中断轮次计为完整通过。最新单源示例及其他独立审查修复后的最终门禁交由并行 Integrator 运行，本方不重复启动。
- 整合方于 2026-10-27T11:06:51Z 至 11:08:22Z 在 darwin/arm64、Go 1.27.3 执行最新整合树的 `bash conformance/run.sh`，报告退出 0；本方已核读 `/tmp/cordis-dx-final-conformance.log`，确认全仓 integration/race、basic-host Native/WASI 和 lifecycle-smoke Native/WASI 通过，两个生命周期报告各有 19 项断言、74 条观察，末尾为 `Full platform acceptance passed.`。该轮包含单源示例、cleanup 字段及配置/Host 审查修复，不是此前轮次的复述；本方未重复执行。代码尚未提交，最后非作者复核仍在进行，任何后续修复须重新验收，不据此宣称最终提交已验证。
