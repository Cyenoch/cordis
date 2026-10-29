# Spec Delta

## REMOVED Requirements

### Requirement: 外部包独立加载
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: Manifest 与产物选择
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 可追溯身份与版本选择
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 包路径与资源限制
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 完整性与信任
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 安装提交与取消
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 包依赖预检
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 初始化与发布
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 可恢复的代码更新
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 停用与卸载
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 中断恢复
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 启用意图与包副本一致
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 安装输入在分配前受限
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 显式候选发现与级联计划
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 可观察的运行恢复
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 面向作者的多格式包声明
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 版本职责清晰且只维护一次
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 显式演进定义级包配置契约
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 元数据与实际构建一致
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 显式选择缺失可分类
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 选择缺省不掩盖失败
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 替换不改变持久启用意图
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 包选择校验响应取消
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 执行副本不做逐文件持久同步
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

### Requirement: 发布者批准完成一次身份迁移
**Reason**: 由本次明确收敛后的组合边界替代。
**Migration**: 使用本变更新增的精简契约；应用行为移至应用服务。

## ADDED Requirements

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
