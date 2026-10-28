# Spec Delta

## ADDED Requirements

### Requirement: 命名空间迁移后可重复配置
宿主 SHALL 能在每次打开存储后按插件清单重复配置同一插件的同名命名空间。已存在命名空间 MUST 保留当前数据版本，配置中的数据版本只作为新建时的初始值。配额变化 SHALL 生效并持久化；新配额低于已用量时 MUST 拒绝且不改变状态。不同插件或不同名称的绑定冲突仍 MUST 被拒绝。

#### Scenario: 迁移后重开
- **WHEN** 宿主以数据版本 1 配置命名空间并迁移到版本 2，关闭存储后重开，再以原清单的版本 1 配置
- **THEN** 配置成功，命名空间数据版本仍为 2，数据不变

#### Scenario: 提高配额
- **WHEN** 宿主以更大配额重新配置已存在的命名空间并重开存储
- **THEN** 超过旧配额但不超过新配额的写入成功

#### Scenario: 配额低于已用量
- **WHEN** 宿主以小于当前已用字节数的配额重新配置命名空间
- **THEN** 配置被拒绝，原配额与数据不变

### Requirement: stderr 限额只限制转发
Runtime SHALL 把 stderr 字节限额用于每个 activation 向宿主转发的总量，而非 activation 的寿命。达到限额后 MUST 继续读取并丢弃后续字节，不得仅因超限终止 activation。诊断回调失败仍 SHALL 可观察，进程回收等待仍 MUST 受 grace 约束。

#### Scenario: 持续日志不终止健康插件
- **WHEN** 插件输出超过配置限额的 stderr 后继续处理请求
- **THEN** 宿主收到的字节不超过限额，健康检查成功；关闭仍完成进程回收

### Requirement: 激活身份在 Policy 生命周期内不可复用
Policy MUST 拒绝已使用的 activation 身份，不得以裁剪历史记录重新允许旧身份。文档 SHALL 明确记录随累计激活次数增长，并随 Policy 生命周期释放。

#### Scenario: 撤销后重用身份
- **WHEN** 一个 activation 已撤销，宿主尝试用相同身份激活
- **THEN** 操作被拒绝，旧租约不恢复
