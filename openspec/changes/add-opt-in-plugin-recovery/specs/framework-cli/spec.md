# Spec Delta

## MODIFIED Requirements

### Requirement: 单一前台宿主
`cordis host` SHALL 独占指定目录、按当前信任显式恢复已启用 entry，并在前台保持运行直到取消；不后台脱离，默认无控制端口，只有显式 control-file 才开放符合 framework-control 的本机认证控制。逐实例 Waiting/Failed SHALL 可诊断，不能报告全部就绪；默认不自动重试，仅显式 recovery 策略文件可启用符合 framework-recovery 的有限恢复。宿主启动本身失败时非零退出；信号退出使用独立清理期限，清理不完整须报告，不等同于业务回滚。

#### Scenario: 前台恢复多实例
- **WHEN** 目录有两个启用 entry，其中一个缺少 provider
- **THEN** 可运行实例正常发布，另一个保持 Waiting 并输出原因；进程持续运行，不因短启动等待到期谎报整体 Active

#### Scenario: 中断与重开
- **WHEN** 用户中断前台运行并再次执行 host
- **THEN** 第一次调用等待关闭与进程回收，保留启用意图；第二次显式恢复并产生新 activation，不重放业务请求

#### Scenario: 显式本机控制
- **WHEN** 用户提供 control-file 并从另一个终端使用 connection 文件
- **THEN** 可通过在线 control 查询和管理既有持久 entry，无须第二次打开 Store；省略 control-file 则不建立监听

#### Scenario: 显式恢复策略
- **WHEN** 用户提供合法 recovery 文件并启动 host
- **THEN** 仅指定持久 native 实例按策略恢复，前台及在线状态展示进度；省略文件仍保持 Failed 等待人工处理
