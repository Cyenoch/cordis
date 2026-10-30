# Proposal

## Why

当前实例进入 Failed 后只支持人工 Update，长时间运行的宿主不能选择自动恢复某个已知可安全重新初始化的插件。用户已确认需要可选恢复，但不需要无限重启或业务重放。

## What Changes

- Host 可按持久 native entry 显式配置有限的指数退避重试；默认关闭，CLI 从显式恢复策略文件加载。
- 重试只针对已完成清理的当前 Failed 激活，使用原配置；预算耗尽保留 Failed，其他实例照常运行。
- 状态可查询尝试次数、下一次计划和停止原因；显式 retry 恢复当前失败实例并重置其预算。
- 手动停止、停用、移除、更新、替换和 Host 关闭均使旧计划失效；不允许迟到重试复活旧实例。
- 不重放业务调用、不恢复内存、不补偿副作用、不自动回退包、不探测业务健康、不默认继承策略给动态 children。

## Capabilities

### New Capabilities

- `framework-recovery`：按实例显式开启的有限故障恢复和人工恢复。

### Modified Capabilities

- `framework-cli`：前台宿主可显式加载恢复策略，默认仍不重试。
- `framework-composition`：默认禁止自动重启，允许显式有限恢复策略。

## Impact

修改 Host options、管理状态和生命周期边界，新增宿主恢复协调逻辑与核心条件重试原语；CLI 接入策略和在线状态。公开契约需要独立审查、race 及五平台 native 故障验收。

实施依赖 `add-local-host-control` 的人工 retry 入口；恢复策略使用同一底层机制。此前暂缓自动恢复的决策因用户确认当前需求而调整，不影响 SDK 或官方 Cordis 兼容声明。
