# Proposal

## Why

Cordis 的插件组合被应用层授权、任务、数据代理、贡献管理和多运行时工具链包围，公开入口重复，维护范围远超依赖激活与资源所有权。此次按用户确认的边界收敛为 Go 插件组合与 native 进程执行。

## What Changes

- **BREAKING** 删除 access 及其 Host、SDK、contract 接口，删除专属协议适配和贡献发布事务。
- **BREAKING** 删除 WASI runtime、worker、构建目标与依赖，仅支持 native。
- **BREAKING** 保留单一 Update 配置更新路径，删除 Retry、Supervise 和计划确认式级联管理。
- **BREAKING** 删除人工配置的 realm 路由；每个 Host 有独立服务注册表，父子实例继续表达资源所有权。
- **BREAKING** 子定义由父 Definition 声明，使用 CreateChild 创建；删除 Host.RegisterDefinition。清理只使用 Scope.Defer；SDK 进程入口只保留 Run。
- 保留包签名、路径校验、调用取消、依赖清理顺序和初始化失败清理。同步收敛未发布 schema，直接删除旧字段，不提供兼容层；签名算法继续用于原生包完整性。
- 更新所有当前代码、文档、示例、验收与主规格，去掉特定宿主引用。只读上游 submodule 不修改。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-composition`: 收敛生命周期、子实例和清理入口，移除人工 realm。
- `framework-execution`: native 单运行时、单一进程入口与标准会话。
- `framework-access`: 整体退出库边界。
- `framework-packages`: 保留完整性校验，删除级联确认、应用数据管理和旧元数据字段。
- `framework-host-integration`: 独立项目文档、示例与验收。

## Impact

影响根包、contract、sdk、runtime、packagehost、CLI、示例、文档站和验收。未发布 Go API 直接删除，不增加兼容别名。包签名仍只证明来源，native 代码具有启动用户权限。本次不引入新沙箱、任务框架或应用存储替代品；上游的依赖与作用域释放语义用于参考，native 协议与包格式属于本实现。
