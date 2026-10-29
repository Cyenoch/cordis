# Proposal

## Why

Cordis 已有 native 加载、依赖收敛、签名包和清理机制，但插件作者缺少连续开发入口，宿主维护者难以关联运行故障和动态子实例，同 Host 的同名服务也无法分组组合。本变更补齐这些可验证的使用场景，不把嵌入式插件框架扩张成守护进程平台，也不以官方 API 数量作为完成标准。

## What Changes

- 增加按实例/激活归属的结构化运行记录与日志：状态变化、启动耗时、退出分类、调用错误、包管理结果；原始 stderr 按显式选择转发，不建设日志数据库或安全审计系统。
- **BREAKING** Host 的查询覆盖持久实例和动态子实例，返回父激活归属；通过受保护句柄支持动态子实例的现有 Wait/Update/Stop，不把它们变成独立持久 entry。
- 增加关闭完成的只读信号，区分“已经排空但有错误”和“清理尚未完成”，供开发循环及宿主判断何时可以继续。
- 增加离线包管理 CLI 和前台 `cordis host`。离线命令只改变下次运行的意图，包含移除已停用 entry 的出口，避免不兼容配置阻塞版本切换；目录被运行中 Host 占用时明确失败。不做 IPC、后台 daemon 或远程管理接口。
- 增加 `cordis dev` 的单作者包 watch → build → 预检 → 停旧启新循环。每次候选使用独立临时 Store，复用现有构建与验签路径，不放宽正式包的同版本内容冲突约束。
- **BREAKING** 增加基于实例父子树的显式、按 service key 隔离；默认仍共享服务。静态 entry 和同包动态子实例使用同一解析规则，生命周期绑定仍固定于 activation。
- **BREAKING** 事件订阅获得独立取消句柄，允许发布后订阅，支持 once/prepend，builtin 与 native 的登记次序和逐监听者分发一致。保留现有三种 dispatch 和可观察错误，不引入跨进程 continuation。
- 复用已有初始化/发布确认、Quiescing 排空、包预检和显式失败恢复；文档讲清手动回退与业务副作用边界。持续扩展现有五平台真实运行验收，而不是重建一套 CI。

## Capabilities

### New Capabilities

- `framework-observability`: 按归属关联的运行诊断、结构化退出记录及显式原始 stderr 通道。
- `framework-cli`: 离线持久包管理、前台运行、单包开发循环及信任/进程所有权边界。

### Modified Capabilities

- `framework-composition`: 按服务隔离、动态子实例查询与控制、可取消的动态事件订阅。
- `framework-execution`: native 订阅控制与逐监听者投递，身份归属及有界诊断。
- `framework-packages`: 无执行的 entry 意图编辑，以及手动替换/回退的明确提交边界。
- `framework-host-integration`: 五平台新增场景、可执行操作指南和能力边界说明。

## Impact

- 核心写集：`contract/`、`internal/core/`、根 Host、`sdk/`、`runtime/internal/process/`、`packagehost/`、`schema/`、`cmd/cordis/`；公共契约和 schema 由 Integrator 统一修改。继续使用标准库 flags 和现有配置解析器。
- 同步调用者、生成模板、测试、验收、中文文档及 GitHub Pages 内容。项目未发布，不添加兼容包装、旧 schema 解析和迁移分支；不修改只读 submodule。
- 官方对照以固定参考 `35fdc969` 为主，替换行为补充参考 `bb3bf8ad`。它们提供事实证据，不自动成为本项目需求；服务 isolate 不等于权限沙箱，官方事件也不默认按祖先路由。
- 当前 `bb29fab` 的 [Conformance run 40903843295](https://github.com/Cyenoch/cordis/actions/runs/40903843295) 已在 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 执行完整脚本并通过。本提案未实现，其新增行为尚无运行证据。

### Non-Goals

不恢复 access/WASI、自动监督重启、业务调用重放；不建设在线控制平面、插件市场或多租户权限；不承诺零停机、自动回退或业务事务回滚。intercept、过滤式作用域事件/global、跨进程 waterfall、原地配置更新、稳定替换句柄、跨包动态挂载、配置树移动、HMR、include/patch/配置回写均不纳入本次任务；已有的静态父子 entry 与同包嵌套 children 保留。完整逐项取舍、原因及重新考虑的条件见 `design.md`。
