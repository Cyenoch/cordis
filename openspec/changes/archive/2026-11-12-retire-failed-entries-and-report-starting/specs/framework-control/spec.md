## MODIFIED Requirements

### Requirement: 控制结果与取消
请求 SHALL 有有限大小与处理期限，复用 Host 的操作互斥、身份验证和错误语义。超过期限或客户端断开只取消等待，不承诺撤销已提交状态；客户端 MUST NOT 自动重发有副作用命令。响应 SHALL 区分已知拒绝、提交后错误和无法确认结果，并提示重新查询。慢控制不得阻塞无关状态查询。

#### Scenario: 操作已提交但响应丢失
- **WHEN** disable 已提交后客户端超时或连接断开
- **THEN** 客户端报告结果无法确认，后续 status 显示真实状态，不自动再次 disable 或声称已回滚

#### Scenario: 并发冲突和坏输入
- **WHEN** 两条控制操作争用同一子树，或请求含未知字段/超限内容
- **THEN** 冲突或输入错误被明确返回，不绕过 Host 预约机制，也不停止无关实例

#### Scenario: 停止成功但带历史错误
- **WHEN** stop 已提交并将实例置为 Stopped，却返回停止过程中的清理失败，或 disable 在部分子项完成后报错
- **THEN** 不根据错误码断言未提交；缺少确定提交证据时报告结果不确定，用户可查询受影响整棵子树

#### Scenario: 停止已失败的 entry
- **WHEN** 对一个已经 Failed 的持久 entry 执行 stop 或 disable
- **THEN** 响应为已完成并附带状态，不因该 entry 之前的失败报告结果不确定
