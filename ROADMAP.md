# Roadmap

阶段顺序以此为准；需求、设计、任务与验证范围见对应 OpenSpec change。

| 阶段 | 入口 |
|---|---|
| 通用插件框架与独立公开交付 | [build-go-plugin-framework](openspec/changes/archive/2026-10-28-build-go-plugin-framework/) |
| 精简组合与 native 执行 | [simplify-native-composition](openspec/changes/archive/2026-10-29-simplify-native-composition/) |
| 完整 native 开发与运维流程 | [complete-native-plugin-workflow](openspec/changes/archive/2026-10-30-complete-native-plugin-workflow/) |
| 插件与 CLI 预发布交付 | [prepare-native-plugin-release](openspec/changes/archive/2026-10-30-prepare-native-plugin-release/) |
| 多插件联合开发 | [add-multi-package-dev](openspec/changes/archive/2026-10-30-add-multi-package-dev/) |
| 本机在线控制 | [add-local-host-control](openspec/changes/archive/2026-10-30-add-local-host-control/) |
| 可选有限故障恢复 | [add-opt-in-plugin-recovery](openspec/changes/archive/2026-10-30-add-opt-in-plugin-recovery/) |
| 进程内类型化组合内核 | [add-typed-composition-kernel](openspec/changes/archive/2026-11-11-add-typed-composition-kernel/) |
| Host 与 native 运行时建立在内核上 | [rebase-native-on-kernel](openspec/changes/archive/2026-11-11-rebase-native-on-kernel/) |
| 跨进程的流式结果与中间件链 | [add-wire-streams-and-chains](openspec/changes/archive/2026-11-11-add-wire-streams-and-chains/) |
| 配置文件驱动的插件树与写回 | [add-config-loader](openspec/changes/archive/2026-11-11-add-config-loader/) |
| 日志服务与定时辅助 | [add-logging-and-timers](openspec/changes/archive/2026-11-11-add-logging-and-timers/) |
| 用 Go 声明契约，推导 schema 与绑定 | [add-go-first-contracts](openspec/changes/archive/2026-11-11-add-go-first-contracts/) |
| 任意对端输入与提供方启停下的加固 | [harden-for-production](openspec/changes/archive/2026-11-11-harden-for-production/) |
| 宿主选择的在途上限与调用观测 | [add-session-limit-and-call-observer](openspec/changes/archive/2026-11-11-add-session-limit-and-call-observer/) |
| 稳定版本发布 | 预发布验收完成后另行提案，不以公开源码或 alpha 版本等同稳定认证 |

应用自行维护业务服务和产品验收，不列入 Cordis 的能力范围。
