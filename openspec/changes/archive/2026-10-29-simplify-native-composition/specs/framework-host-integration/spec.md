# Spec Delta

## REMOVED Requirements

### Requirement: 独立消费
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 通用宿主端到端验收
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 宿主协议可适配
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 外部宿主 隔离执行场景
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 外部宿主 贡献和事务所有权
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 兼容性来源与覆盖报告
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 跨平台发布验证
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 可复现的持续验收与公开文档
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 可检索的中文文档站与自动发布
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 完整使用手册与可执行实操
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 文档代码基线与同步反馈
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 构建期文档变量
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 开发者站点内容边界
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 统一的严格配置文件入口
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 文件格式能力诚实表达
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 标准编辑器配置体验
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 通用分发可显式组合
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 无兼容胶水的统一开发者交付
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: YAML 数字识别不接受双重符号
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 错误分类不依赖文案
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 未发布 API 不保留兼容包装
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

## ADDED Requirements

### Requirement: 独立项目与文档
公开文档、代码和验收 SHALL 只描述 Cordis 自身契约，不依赖或命名具体宿主应用。文档与示例 MUST 使用当前 API。只读上游参考源码不修改。

#### Scenario: 独立检出
- **WHEN** 仅检出 Cordis 并运行文档示例和验收
- **THEN** 无需相邻应用仓库或专属协议适配器

### Requirement: 可复现验收
通用验收 SHALL 覆盖 Go 测试、race、独立 native 构建与生命周期运行。报告 MUST 只声明实际运行过的平台，Windows 入口与 POSIX 入口保持相同范围。

#### Scenario: 执行验收
- **WHEN** 在当前开发平台执行通用验收脚本
- **THEN** 实际构建原生插件并验证双实例、依赖、更新与清理，输出真实平台

### Requirement: 文档工具链
中文文档站 SHALL 保留导航、搜索、构建变量与代码示例校验；删除的 API 不得继续作为教程入口。

#### Scenario: 构建文档
- **WHEN** 文档站执行检查和生产构建
- **THEN** 内部链接和当前示例通过校验，删除页面不出现在导航中
