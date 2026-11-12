## MODIFIED Requirements

### Requirement: 隔离边界的所有权和持久恢复
隔离声明 SHALL 随 entry 登记固定；静态声明持久保存并在 Restore 时重建，动态子实例声明绑定父激活并沿用同包限制。Update 只更新业务配置，不隐式移动边界。宿主 SHALL 可显式选择某个当前持久 entry 的服务解析视图，在其中调用服务，或取得按版本范围校验过的服务端点交给生成的客户端，一元与流式方法都可调用；端点指向取得时的 provider。视图 SHALL 绑定取得时的那次登记：entry 停用、移除或被替换后，视图 MUST 明确失败，不退回根空间解析；同一登记的恢复重启不使视图失效。视图不是插件身份，不获得清理绕过资格。

#### Scenario: 持久子树恢复
- **WHEN** 配有 db@1 隔离声明的静态父子树关闭后重开并 Restore
- **THEN** 仍按原声明形成两组服务绑定，不把旧 activation 的 provider 带入新实例

#### Scenario: 宿主定位同名服务
- **WHEN** 宿主从第一组某实例的解析视图调用 db@1，再使用默认根调用
- **THEN** 前者到第一组 provider，后者仅查根空间；不能任取一个同名 provider

#### Scenario: 隔离 entry 的流式端点
- **WHEN** 同一包的两个 entry 各自隔离并提供带流式方法的同名服务，宿主从每个 entry 的视图取得端点读取流，读到一部分后停止
- **THEN** 每条流只产生该 entry 的结果；停止读取取消该 entry 的流处理；根作用域看不到这两个 provider

#### Scenario: 视图的版本范围
- **WHEN** 宿主从 entry 的视图按不包含 provider minor 的版本范围取端点
- **THEN** 得到 incompatible 错误，指出要求的范围与可见的版本

#### Scenario: 旧视图明确失败
- **WHEN** 宿主取得 entry 的视图后，该 entry 被移除，再以同一标识重新启用
- **THEN** 旧视图对隔离与未隔离的服务都以 not_ready 失败并指出 entry；重新取得的视图解析到新实例的 provider
