## ADDED Requirements

### Requirement: 应用错误码原样传递

服务处理函数返回带错误码的错误时，响应 SHALL 原样携带该错误码与消息；没有错误码的错误 SHALL 按类别送达：取消为 `canceled`，超时为 `timeout`，其余为 `plugin_failed`。框架 MUST NOT 改写或拒绝应用自定义的错误码。发布的会话 schema SHALL 接受任意字符串错误码，并拒绝非字符串的错误码。

#### Scenario: 应用自定义错误码

- **WHEN** 处理函数以错误码 `game_running` 失败
- **THEN** 调用方收到的响应错误码为 `game_running`，该响应通过会话 schema 校验

#### Scenario: 错误码不是字符串

- **WHEN** 一个响应的 `error.code` 是数字
- **THEN** 会话 schema 拒绝该响应
