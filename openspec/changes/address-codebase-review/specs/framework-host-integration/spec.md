# Spec Delta

## ADDED Requirements

### Requirement: YAML 数字识别不接受双重符号
YAML 纯量 SHALL 只在至多一个前导符号时被识别为数字；带两个前导符号的纯量 MUST 作为字符串保留，且两种符号顺序结果一致。

#### Scenario: 双重符号的进制数
- **WHEN** YAML 配置包含纯量 `+-0x10`、`-+0x10` 或 `+-0o7`
- **THEN** 三者都解析为字符串，不产生数字

### Requirement: 错误分类不依赖文案
contract.Error SHALL 支持 errors.Is 按相同 Code 匹配，并保留取消及超时与标准 context 错误的匹配。未知字段解码错误 SHALL 提供可供 errors.As 读取的字段名，配置定位 MUST 使用该类型信息而非解析错误字符串。

#### Scenario: 未知字段带定位信息
- **WHEN** 配置解码因未知字段失败，错误又被包装
- **THEN** 调用方仍能通过 errors.As 获取字段名，配置错误定位到该字段

### Requirement: 未发布 API 不保留兼容包装
错误投影 SHALL 只使用 access.PublicError 包函数；包选择预检 SHALL 直接使用 Store.CheckSelect(ctx, ref, opt)，不保留旧签名或重复的 Context 后缀入口。没有调用方且无独立用途的 Limits、OpenWithLimits、TaskManager.Lookup MUST 移除；Policy.Calls SHALL 只返回查询结果。通用进程层 MUST NOT 硬编码宿主私有的 plugin.shutdown 方法名，自定义生命周期通过 typed PluginSession 映射。

#### Scenario: 单一预检入口响应取消
- **WHEN** 宿主通过 Store.CheckSelect 预检并取消 context
- **THEN** 操作停止校验且不改变选择，错误投影不需要 Sink 实例
