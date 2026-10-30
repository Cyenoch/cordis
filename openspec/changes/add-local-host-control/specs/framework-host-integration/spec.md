# Spec Delta

## MODIFIED Requirements

### Requirement: 公开能力边界
文档 SHALL 按可观察场景说明与固定版本官方 Cordis 的对应与差异，不宣称完全兼容。明确服务隔离不是权限边界、事件仍默认 Host 广播、替换重建 activation、动态 child 不单独持久化，以及离线 CLI 不控制运行中应用；本机在线控制必须显式授权。跨进程 waterfall、intercept、远程管理、HMR 与配置回写不得被文案暗示已实现。

#### Scenario: 查询同名服务组合教程
- **WHEN** 用户阅读双组同名服务的教程
- **THEN** 能识别哪些 service key 被隔离、哪些仍共享，以及事件未随服务自动隔离，不需要猜测父子关系是否自带隔离

#### Scenario: 选择管理入口
- **WHEN** 用户阅读运行管理教程
- **THEN** 能分别执行空闲 Store 的离线命令和运行中 Host 的本机认证控制，不需要绕过锁或猜测端口；文档说明连接凭据不授予 native 沙箱能力
