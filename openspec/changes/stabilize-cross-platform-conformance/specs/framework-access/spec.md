# Spec Delta

## ADDED Requirements

### Requirement: stderr 采集完整且严格限额
显式启用 stderr 采集时，activation 在进程退出前写入且未超过限额的 stderr SHALL 在进程树回收后的 grace 内持续交付给宿主诊断出口，不得因拆除顺序丢弃；会话报告结束时交付已完成或已到 grace 上界。转发给宿主的字节总数 MUST 不超过配置的 stderr 限额；超限仍终止 activation。仍持有 stderr 的逃逸写端 MUST NOT 让拆除等待超过 grace。

#### Scenario: 慢诊断出口与进程退出竞争
- **WHEN** 插件写出最后一段 stderr 后立即退出，而宿主诊断出口在 grace 内处理完上一段
- **THEN** 会话结束前最后一段已交付，Close 不提前返回

#### Scenario: stderr 超限
- **WHEN** 插件持续写出超过限额的 stderr
- **THEN** activation 被终止且失败原因可观察，宿主收到的 stderr 不超过限额
