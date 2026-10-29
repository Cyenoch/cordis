# framework-host-integration Specification

## Purpose

Cordis 作为独立 MIT Go module 提供代码、中文文档、CLI 与可复现验收，不引用具体应用项目，示例只演示通用组合和 native 插件开发。

## Requirements

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
