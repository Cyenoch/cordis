# Spec Delta

## Purpose

为明确允许重复初始化的持久 native 实例提供有限的自动故障恢复，并让人工干预优先于旧重试计划。恢复只重建插件激活，不承担业务请求重放、外部事务补偿、健康探测或代码回滚。

## ADDED Requirements

### Requirement: 显式按实例启用恢复
Host SHALL 只对明确配置恢复策略的持久 native entry 调度重试，策略包含正数最大重试次数、初始延迟和不小于初始值的最大延迟。默认关闭；动态 child 和 builtin 不自动继承策略。CLI SHALL 从显式指定的严格策略文件加载，非法策略在启动插件前失败。不存在的 entry 必须可诊断，不能悄悄套用到其他实例。

#### Scenario: 默认失败行为
- **WHEN** 未配置策略的插件初始化失败或运行中崩溃
- **THEN** 实例保持 Failed，直到显式操作，不产生自动 activation

#### Scenario: 只恢复指定实例
- **WHEN** 同包两个持久实例均失败，但策略只列出其中一个
- **THEN** 只对被列出的实例安排重试，不按包 ID 扩散，也不单独重建其动态 child

### Requirement: 有限指数退避
自动恢复 SHALL 仅对清理完成、仍启用且未临时停止的当前 Failed activation 执行。第 n 次重试等待 min(初始延迟乘以 2 的 n-1 次方, 最大延迟)，计算不得溢出；初次启动不计入重试数。每次已接受的自动重试消耗一次预算；短暂 Active 不重置预算，耗尽后保持 Failed 并标记 exhausted。未完成清理或存在清理错误的激活不得自动恢复。

#### Scenario: 连续崩溃
- **WHEN** 最大重试数为三，插件每次启动后都再次失败
- **THEN** 最多自动产生三次新 activation，延迟按策略递增并封顶，随后停止；不能靠短暂 Active 无限刷新额度

#### Scenario: 清理仍在进行或失败
- **WHEN** 失败插件进入 Quiescing，或已结束但结果包含清理错误
- **THEN** 不并行启动新 activation；前者等待真实清理完成，后者显示 cleanup_failed 并保留人工处理入口

#### Scenario: 缺少服务与业务错误
- **WHEN** 实例为 Waiting，或单次业务调用返回错误而实例仍 Active
- **THEN** 不消耗恢复预算，也不因该错误重启或重放调用

#### Scenario: 历史激活之后图校验失败
- **WHEN** 实例有历史 activation，后来因依赖环进入没有新 activation 的 Failed
- **THEN** 不把旧身份或退出记录当成本轮可重试故障，不调度自动重启或消耗额度

### Requirement: 人工操作优先及旧计划失效
自动重试 SHALL 在提交时原子确认原登记、activation、配置 revision 和恢复计划仍有效。stop、disable、remove、Update、Replace、人工 retry 及 Host 关闭 SHALL 使对应旧计划失效；不能用过时配置或句柄复活新登记。人工 retry 只在当前 Failed 实例接受后刷新预算；显式配置更新、重新登记和重开 Host 建立新的恢复周期，单纯查询与恢复已有登记不刷新。

#### Scenario: 等待期间人工停止
- **WHEN** 退避计时期间用户 stop 或 disable 实例
- **THEN** 旧计划取消；即使计时器随后触发也不启动插件，原启停意图保持正确

#### Scenario: 同 ID 已被替换
- **WHEN** 计划针对的实例被移除并重新登记，或配置已显式更新
- **THEN** 旧计划不能控制新登记或覆盖新配置；新周期仅按新状态和既定策略判断

#### Scenario: 人工变更在提交前被拒绝
- **WHEN** 退避期间用户提交非法配置或未通过 Replace 预检的包，原实例状态没有改变
- **THEN** 原恢复计划、预算和计划时间保留，不因被拒绝请求取消或延迟已有恢复

#### Scenario: 预算耗尽后人工恢复
- **WHEN** 用户明确 retry 一个 exhausted 的 Failed 实例
- **THEN** 只提交一次新 activation 并刷新该实例后续自动预算；其他实例的额度不变

### Requirement: 恢复状态和关闭可观察
恢复 SHALL 提供当前 entry 对应的尝试次数、预算、下次重试时间和恢复状态，结构化日志带原失败及新 activation 的归属。预算只在当前 Host 生命周期内维护，不伪装成持久崩溃历史。Host 关闭 SHALL 先停止调度并等待恢复工作退出，再释放资源；关闭完成信号不得早于恢复工作结束。

#### Scenario: 查看退避和耗尽
- **WHEN** 用户通过 Host 或本机在线 status 查询恢复中的实例
- **THEN** 可区分 scheduled、retrying、exhausted、cleanup_failed 和未启用恢复；原生命周期状态及退出原因仍可见

#### Scenario: 退避中关闭 Host
- **WHEN** Host 在等待下一次重试时被关闭
- **THEN** 不再创建新 activation，后台恢复工作退出，关闭信号符合真实清理完成情况

#### Scenario: 重开不是重放
- **WHEN** 用户关闭并重新打开 Host
- **THEN** 显式 Restore 重建启用实例，恢复预算重新开始；既有运行历史保留，任何历史业务请求均不重放
