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
通用验收 SHALL 覆盖 Go 测试、race、独立 native 构建与生命周期运行。报告 MUST 只声明实际运行过的平台，Windows 入口与 POSIX 入口保持相同范围。声明支持的 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 SHALL 在对应平台真实运行验收，记录准确提交与 CI 链接，不能以交叉编译替代执行。

#### Scenario: 执行验收
- **WHEN** 在当前开发平台执行通用验收脚本
- **THEN** 实际构建原生插件并验证双实例、依赖、更新与清理，输出真实平台

#### Scenario: 新能力跨平台验证
- **WHEN** 本变更准备声明完成
- **THEN** 五平台实际验证签名包安装、CLI 意图与前台运行、native 崩溃分类、dev 编辑重启、作用域绑定、动态 child 控制及事件取消；报告逐项成功/失败/未执行，不沿用旧提交结果冒充新增能力通过

### Requirement: 文档工具链
中文文档站 SHALL 保留导航、搜索、构建变量与代码示例校验；删除的 API 不得继续作为教程入口。

#### Scenario: 构建文档
- **WHEN** 文档站执行检查和生产构建
- **THEN** 内部链接和当前示例通过校验，删除页面不出现在导航中

### Requirement: 可执行的开发与运维流程
文档 SHALL 提供可执行的签名包离线管理→前台运行、单包 dev、故障定位、动态子树检查、显式失败恢复和版本回退流程。说明日志包含用户错误文本的风险、原始 stderr 的显式授权、关闭与强制终止限制；不把资源清理或回退代码表述成业务回滚。

#### Scenario: 无 Go 宿主代码的首次运行
- **WHEN** 新用户按教程构建插件、打包签名、配置公钥授权、安装选择并启用后执行 host
- **THEN** 能运行插件并看到其归属状态；中断后退出，离线 status 不再将最近运行记录称为当前运行状态

#### Scenario: 真实故障定位
- **WHEN** 教程中的测试插件初始化失败或非预期退出
- **THEN** 用户能从结构化记录定位包、entry、activation 和阶段，并按教程手动处理；没有无条件自动重启或重放

### Requirement: 公开能力边界
文档 SHALL 按可观察场景说明与固定版本官方 Cordis 的对应与差异，不宣称完全兼容。明确服务隔离不是权限边界、事件仍默认 Host 广播、替换重建 activation、动态 child 不单独持久化，以及离线 CLI 不控制运行中应用。跨进程 waterfall、intercept、在线管理、HMR 与配置回写不得被文案暗示已实现。

#### Scenario: 查询同名服务组合教程
- **WHEN** 用户阅读双组同名服务的教程
- **THEN** 能识别哪些 service key 被隔离、哪些仍共享，以及事件未随服务自动隔离，不需要猜测父子关系是否自带隔离
