# Spec Delta

## ADDED Requirements

### Requirement: 在途准入只计有效请求
接收端 SHALL 只把尚未被对端取消的请求计入在途上限；对端已发送取消的请求在其 handler 返回前 MUST NOT 占用准入额度。真正超过在途上限的请求仍 MUST 被拒绝。

#### Scenario: 超时洪泛后的健康调用
- **WHEN** 宿主发出等于在途上限的调用，全部超时并发送取消，而插件 handler 仍在退出中，随后宿主发出一次健康检查
- **THEN** 健康检查成功，会话保持可用，插件不被判为协议违规

#### Scenario: 真实超额
- **WHEN** 对端在已有等于上限的有效请求时再发送一个新请求
- **THEN** 新请求被拒绝并报告超出在途上限

### Requirement: 取消后尚未退出的请求有独立资源上限
接收端 SHALL 独立约束已取消但 handler 尚未退出的请求，最多保留四倍在途上限；超限 MUST 显式关闭会话，不得允许原始 peer 通过反复 request/cancel 无限累积 handler。该预算 MUST NOT 挤占仍有效请求的普通准入名额。

#### Scenario: 原始 peer 取消洪泛
- **WHEN** 原始 peer 持续发送新 ID 的 request/cancel，而对应 handler 尚未完成退出，数量超过四倍在途上限
- **THEN** 会话被关闭，后续请求不进入 handler

### Requirement: v1 通用 Runtime 使用一致的在途预算
通用 v1 Runtime SHALL 使用 SDK 的 64 个在途请求预算；显式配置其他值 MUST 在创建进程前返回配置错误。自定义 SessionFactory MUST 保留其自有协议的预算策略。低层 wire endpoint 的非默认预算 SHALL 由两端调用者保证一致，不新增 v1 帧字段。

#### Scenario: 启动前拒绝不兼容预算
- **WHEN** 宿主为通用 Runtime 配置在途上限 8 并启动插件
- **THEN** 启动返回配置错误，不创建随后会被第 9 个并发请求误杀的会话

### Requirement: 无关实例清理互不占用全局槽位
一个 activation 的合作式清理等待 MUST NOT 占满全局固定槽位而阻止无关实例清理。框架 MUST NOT 声称能强制终止进程内不合作的 callback；Host.Close 仍须等待其拥有的资源清理。

#### Scenario: 八个清理阻塞时停用无关实例
- **WHEN** 八个 builtin 的清理均阻塞，另一个无关实例被 Stop
- **THEN** 无关实例的清理可以执行并完成
