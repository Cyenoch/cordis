# Tasks

本清单是后续实施计划，全部保持未完成；临时诊断不是修复验收。先于 `negotiate-session-frame-budgets` 实施，共享 wire 和并行生命周期控制由 Integrator 串行集成。

## 1. Wire 响应隔离与协议说明

写集：`wire/`、`docs/protocol.md`；owner 为 Integrator / wire。

- [ ] 1.1 把双向超大成功结果、超大 coded error 和无关在途调用的失败复现转为真实 endpoint 行为测试；记录修复前失败，验证无原响应字节写出、后续调用成功且 activation 不变。
- [ ] 1.2 在唯一编码路径增加本地尺寸分类，普通响应至多发送一次固定 `plugin_failed` 小错误；验证实际 JSON+LF 的 N/N+1、转义膨胀和同关联错误，不用 message/code 猜测本地失败阶段。
- [ ] 1.3 复用原取消准入与监督处理编码期间取消、迟到错误；验证取消/超时不被尺寸错误或迟到成功覆盖，正常确认不误关会话，未确认取消仍有界失败。
- [ ] 1.4 保留 initialize、health、shutdown、activation.publish、activation.retire 和初始化前反向响应的生命周期边界；验证超限初始化不发布、shutdown/retire 不伪造成功，也不重放已执行副作用；断链 disposer 失败必须可观察，不能断言远端释放成功。
- [ ] 1.5 覆盖小错误也装不下、无效 JSON、部分写入报错、零进展写入、发送准入背压和实际收到违规帧；验证显式会话失败且不递归替代；本地大请求另验仅调用失败且远端未执行。
- [ ] 1.6 同步协议文档的完整帧计量、错误码/安全固定说明、控制与业务边界和预算尚待统一限制；运行 `go test -race ./wire`，核对文档未把入队成功写成对端已收到。

## 2. SDK 与真实运行时回归、作者指南

写集：SDK tests、Runtime integration fixtures、独立作者/Host 验收、`docs/plugin-author.md`、`docs/troubleshooting.md`；不重复改 wire 或 contract。

- [ ] 2.1 使用真实 SDK ServeTransport 覆盖超大结果与错误，确认进程入口不退出、后续正常请求成功；保留实例内业务串行，不为测试绕过并发契约，运行对应 SDK 测试。
- [ ] 2.2 扩充独立 Native/WASI 作者包验收：执行一次有计数的调用后让结果超限，再读回计数与 activation；验证不重启、不重试、不重复副作用，健康邻居不受影响。
- [ ] 2.3 更新单记录超限、转义、分页/投影选择和“错误不代表回滚”的作者示例与排障说明；按示例运行真实构建/调用，保留并行文档元数据并记录实际平台。

## 3. 集成审查与验收记录

写集：本 change 与最终集成记录；不把归档当作实施完成任务。

- [ ] 3.1 非作者审查尺寸分类、取消竞态、控制方法及业务副作用语义，确认没有旧行为开关、payload 泄露或跨层第二套发送器；在本 change 记录实际发现和处置。
- [ ] 3.2 运行相关 race 测试、`bash conformance/run.sh`（实际 Windows 验证使用 `conformance/run.ps1`）及当前 docs 校验，记录成功、跳过和不可运行范围；严格验证本 change 后才勾选已完成任务。

## Workflow follow-up

- 整体预算交付继续实施 `negotiate-session-frame-budgets`，不把本步骤单独描述为已经支持不同宿主预算。
- 经用户要求归档时先建立 build 主规格基线，再应用本 delta，保留并行新增场景。
