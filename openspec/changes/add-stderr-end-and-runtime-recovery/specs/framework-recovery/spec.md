## MODIFIED Requirements

### Requirement: 显式按实例启用恢复
Host SHALL 只对明确配置恢复策略的持久 native entry 调度重试，策略包含正数最大重试次数、初始延迟和不小于初始值的最大延迟。默认关闭；动态子实例和进程内插件不适用策略。策略 SHALL 可在打开 Host 时给出，也可在 Host 运行中为已保存的 entry 登记、修改或清除，两者校验与语义相同；登记或修改开始新的恢复周期；以相同策略再次登记 MUST NOT 改变当前周期。删除 entry SHALL 清除它的策略，同 ID 重新启用的 entry 不沿用旧策略。CLI SHALL 从显式指定的严格策略文件加载，非法策略在启动插件前失败。不存在的 entry 必须可诊断，不能悄悄套用到其他实例。

#### Scenario: 默认失败行为
- **WHEN** 未配置策略的插件初始化失败或运行中崩溃
- **THEN** 实例保持 Failed，直到显式操作，不产生自动 activation

#### Scenario: 只恢复指定实例
- **WHEN** 同包两个持久实例均失败，但策略只列出其中一个
- **THEN** 只对被列出的实例安排重试，不按包 ID 扩散，也不单独重建其动态 child

#### Scenario: 运行中新建的 entry
- **WHEN** Host 运行中安装并启用一个 entry，它启动失败后宿主为它登记策略
- **THEN** 恢复按策略重试它，与打开 Host 时配置的策略相同；修改策略后恢复状态显示新预算且已用次数归零，清除后该 entry 不再出现在恢复状态中，此后的失败不再重试

#### Scenario: 非法或不存在
- **WHEN** 运行中为未保存的 entry、非 native 包的 entry 登记策略，或策略的次数、延迟非法
- **THEN** 登记以 invalid 失败，已有策略不变

#### Scenario: 删除 entry
- **WHEN** 有恢复策略的 entry 被删除，随后以相同 ID 重新启用
- **THEN** 恢复状态中不再有该 entry，新 entry 失败后不被自动重试

### Requirement: 有限指数退避
自动恢复 SHALL 仅对清理完成、仍启用且未临时停止的当前 Failed activation 执行。第 n 次重试等待 min(初始延迟乘以 2 的 n-1 次方, 最大延迟)，计算不得溢出；初次启动不计入重试数。每次已接受的自动重试消耗一次预算；短暂 Active 不重置预算，耗尽后保持 Failed 并标记 exhausted。未完成清理或存在清理错误的激活不得自动恢复。启动时包被 Host 自己的信任或完整性校验拒绝的激活 MUST NOT 自动重试：保持 Failed 并标记 denied，不再安排重试；插件自己报告的错误码不在此列。

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

#### Scenario: 信任校验拒绝
- **WHEN** 有恢复策略的 entry 启动时，它的包不再被当前信任策略接受
- **THEN** 恢复状态为 denied，带拒绝原因，已用次数为零且不安排下次重试；宿主批准该包后人工 retry 开始新周期

### Requirement: 恢复状态和关闭可观察
恢复 SHALL 提供当前 entry 对应的尝试次数、预算、下次重试时间、恢复状态和本周期内最近一次失败的错误，结构化日志带原失败及新 activation 的归属。预算只在当前 Host 生命周期内维护，不伪装成持久崩溃历史。Host 关闭 SHALL 先停止调度并等待恢复工作退出，再释放资源；关闭完成信号不得早于恢复工作结束。

#### Scenario: 查看退避和耗尽
- **WHEN** 用户通过 Host 或本机在线 status 查询恢复中的实例
- **THEN** 可区分 scheduled、retrying、exhausted、cleanup_failed、denied 和未启用恢复，并看到最近一次失败的错误；原生命周期状态及退出原因仍可见

#### Scenario: 退避中关闭 Host
- **WHEN** Host 在等待下一次重试时被关闭
- **THEN** 不再创建新 activation，后台恢复工作退出，关闭信号符合真实清理完成情况

#### Scenario: 重开不是重放
- **WHEN** 用户关闭并重新打开 Host
- **THEN** 显式 Restore 重建启用实例，恢复预算重新开始；既有运行历史保留，任何历史业务请求均不重放
