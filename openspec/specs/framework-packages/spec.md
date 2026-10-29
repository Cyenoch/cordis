# framework-packages Specification

## Purpose

定义 native 插件包的作者声明、不可变安装、来源校验与显式版本选择；格式与当前实现同步收敛，包管理不接管应用数据，不提供历史兼容。

## Requirements

### Requirement: Native 包与格式边界
作者工具 SHALL 只生成 native 包。schema 与 Go 契约 MUST 同步删除应用字段与其他 runtime；不存在旧格式或 API 的兼容包装。签名仍覆盖全部包内容。

#### Scenario: 不受支持的包声明
- **WHEN** 包要求非 native runtime 或应用能力
- **THEN** 在执行前报告不兼容，不静默忽略执行要求

### Requirement: 包完整性与独立加载
包 SHALL 由独立 module 构建，安装校验路径、大小、摘要及签名，启动使用不可变快照。开发模式必须显式选择确定摘要；签名只证明来源，不限制进程权限。

#### Scenario: 签名包被篡改
- **WHEN** 签名后的某个产物被替换
- **THEN** 安装失败，原有选中包与运行实例不变

### Requirement: 显式包管理
包 SHALL 通过安装、选择、替换和卸载显式管理；有反向依赖或仍在使用的包不得隐式级联破坏。框架不提供计划确认式级联 API。

#### Scenario: 卸载仍使用的包
- **WHEN** 应用尝试卸载被运行实例或启用依赖使用的包
- **THEN** 返回冲突；应用先显式停用相应实例后再执行

### Requirement: 持久意图与恢复
有效配置与启用意图 SHALL 原子记录，重开后由应用显式恢复。重复恢复复用已有登记而不重试失败实例；运行中断可查询，不冒充业务事务回滚。

#### Scenario: 重复恢复
- **WHEN** 重开 Host 并连续两次恢复相同启用记录
- **THEN** 复用同一实例，不重复运行正常激活
