## MODIFIED Requirements

### Requirement: 提供方消失时的调用结果

调用或流进行中，提供方的 entry 停止或失败时，调用方 SHALL 得到 `not_ready`，错误文本带 entry 离开的原因；它与提供方自己回答的失败可以区分。事件投递与链的步骤同样如此。调用方 MUST 在有限时间内返回，MUST NOT 收到不带分类的传输错误。native 进程未经 shutdown 退出时，原因 SHALL 带平台提供的退出码或信号；对端在 stdout 上写入非协议内容时，原因 SHALL 指出 stdout 只承载协议帧。

#### Scenario: 调用期间反复停用再启用提供方

- **WHEN** 多个调用方持续调用服务和读取流，同时提供方被反复停用再启用
- **THEN** 每次调用要么成功，要么以带分类的错误、服务不可用或取消结束；全部调用在限期内返回，启动过的激活都完成清理，之后的调用成功

#### Scenario: 提供方在调用中退出

- **WHEN** native 提供方在处理调用时以退出码 7 退出
- **THEN** 调用方得到 `not_ready`，错误文本含退出码 7，而不只是传输层的 EOF

#### Scenario: 插件把日志写到 stdout

- **WHEN** native 插件在会话中往 stdout 写入一行普通文本
- **THEN** 会话以协议失败结束，错误文本指出 stdout 只承载协议帧
