## MODIFIED Requirements

### Requirement: 显式信任与可组合输出
install/select/enable 和前台 host SHALL 使用显式指定的信任文件，采用已有公钥、publisher/包 ID 授权、撤销语义；list/status/disable/remove/uninstall 不要求信任文件，但仍须取得独占目录。不得从包自签名信息推导信任。生产管理不支持全局跳过验签；run/dev 的开发许可只限本次精确摘要。命令结果输出 JSON 至 stdout，诊断写 stderr，沿用 0/1/2 退出码；CLI 不推断多层环境配置。run 与 dev SHALL 把插件 stderr 转发到 CLI 的 stderr，每行标明来源 entry 与截断；host MUST 仅在显式开启时转发。

#### Scenario: 未授权签名
- **WHEN** 用户安装一个签名有效但签名者不在指定信任文件中的包
- **THEN** 安装失败，原有选中版本和 entry 不变；CLI 不自动导入该签名者

#### Scenario: 输出写入失败
- **WHEN** 已提交的离线 enable 在写 stdout 时失败
- **THEN** 命令报告输出失败及已提交事实，不把记录删除或自动重复 enable

#### Scenario: 开发时看到插件崩溃
- **WHEN** 用 run 调用的方法 panic，插件进程退出
- **THEN** stderr 含 panic 文本，行首标明 entry；命令非零退出

#### Scenario: 生产宿主默认不转发
- **WHEN** 未显式开启转发的 host 运行一个写 stderr 的插件
- **THEN** 插件输出不出现在 CLI 输出中

### Requirement: 单一前台宿主
`cordis host` SHALL 独占指定目录、按当前信任显式恢复已启用 entry，并在前台保持运行直到取消；不后台脱离，默认无控制端口，只有显式 control-file 才开放符合 framework-control 的本机认证控制。逐实例 Waiting/Failed SHALL 可诊断，不能报告全部就绪；默认不自动重试，仅显式 recovery 策略文件可启用符合 framework-recovery 的有限恢复。宿主启动本身失败时非零退出；信号退出使用独立清理期限，默认 SHALL 覆盖一次用尽宽限的 native 停止；清理不完整须报告并保留仍在使用的目录，不等同于业务回滚。

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

#### Scenario: 插件慢关
- **WHEN** 插件收到停止后用掉宽限才退出，用户中断 host 或 run
- **THEN** 默认期限内完成回收，不留孤儿进程和临时目录；期限被调短而未完成时，命令说明清理未完成并保留目录
