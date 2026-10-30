# Spec Delta

## MODIFIED Requirements

### Requirement: 统一配置更新
Update SHALL 是配置更新的唯一入口，同时支持失败后重新初始化；Host 另提供保持当前配置的显式 Retry，仅对当前静态 Failed 登记生效。配置先校验再提交；并发更新收敛到最新 revision。框架 SHALL 默认不自动重启失败实例，仅显式配置的 framework-recovery 策略可有限重试；任何路径 MUST NOT 重放业务调用。

#### Scenario: 无效配置与显式恢复
- **WHEN** 未配置自动恢复的运行实例收到非法配置，随后一次有效更新的初始化失败，再次提交有效配置
- **THEN** 非法配置保留原实例；初始化失败可观察，后一次 Update 可恢复且没有后台重试

#### Scenario: 保持配置人工重试
- **WHEN** 当前静态实例进入 Failed 且清理完成，宿主明确 Retry
- **THEN** 使用当前配置建立新 revision 和 activation，不修改配置意图；被替换的旧登记不得控制新实例
