## MODIFIED Requirements

### Requirement: 可观察控制
实例 SHALL 提供状态、等待、Update 和 Stop；Host 提供显式启用、停用、恢复、查找和关闭。查找及枚举 SHALL 包括动态子实例，区分持久/动态来源并提供完整父激活身份；动态子实例句柄支持现有控制但不产生持久意图。长等待不占全局管理准入。取消等待 SHALL 不撤销已提交的控制意图。Stop、停用、删除与替换 SHALL 只返回停止过程中发生的错误：停止一个已经 Failed、或已在为记录的失败清理的实例 MUST NOT 返回该失败，它经状态结果与运行历史可见。Waiting 状态 SHALL 带至少一个等待原因；没有可报告原因的启动窗口报 Starting。恢复临时停止的 entry SHALL 与启用后的等待一致：等到实例 Active、Failed 或停止才返回，并返回启动失败的错误；已失败的 entry 恢复时重新启动，同样报告重启的结果。

#### Scenario: 等待服务期间管理其他实例
- **WHEN** 一个实例等待缺失服务，同时登记该服务 provider
- **THEN** provider 可登记并使等待实例启动，不被等待操作阻塞

#### Scenario: 查询并停止动态子树
- **WHEN** 父创建子和孙实例，宿主枚举后通过子实例句柄 Stop
- **THEN** 枚举提供来源、父激活与等待原因；Stop 只停止该子树，父和兄弟继续运行，catalog 不新增独立 child 记录

#### Scenario: 旧句柄不控制重建子实例
- **WHEN** 父重启后以相同 ID 重建子实例，宿主持有上次的子句柄
- **THEN** 旧句柄的 Update/Stop 被拒绝，不影响新实例；宿主重新查找得到新父激活所属句柄

#### Scenario: 清理中子实例仍可见
- **WHEN** 父正在退役且子清理尚未完成
- **THEN** 枚举仍显示该子实例及 Quiescing/阻塞状态，清理并移除完成后才消失；最终消失不抹掉已有终态诊断

#### Scenario: 停用和删除已失败的 entry
- **WHEN** 一个持久 entry 启动失败后被停用，另一个启动失败的 entry 被删除
- **THEN** 停用返回成功且 entry 保存为停用、不再登记；删除返回成功且持久记录被删除；两者的失败仍在运行历史中

#### Scenario: 停止已失败的实例
- **WHEN** 宿主临时停止一个已失败的持久 entry，或停止一个启动失败的动态子实例
- **THEN** Stop 返回成功；持久 entry 报 Stopped 且结果中仍有失败分类，动态子实例不再登记

#### Scenario: 停止过程中出错
- **WHEN** Active 实例在停用或临时停止时清理失败，或调用方的期限在停止完成前到达
- **THEN** 停用或 Stop 返回该错误，不因实例随后进入 Failed 而当作历史失败剔除

#### Scenario: 刚启用的 entry
- **WHEN** 宿主启用一个依赖已满足的 entry 后立即查询状态，随后启用一个缺少服务的 entry 并反复查询
- **THEN** 前者报 Starting 或 Active，Starting 时 Applied 小于 Desired；后者在报 Waiting 时带有缺少该服务的原因，任何时刻不报没有原因的 Waiting

#### Scenario: 恢复已失败的 entry
- **WHEN** 宿主临时停止一个启动失败的持久 entry 后恢复它，一次重启仍失败，另一次重启成功
- **THEN** 前一次恢复返回启动失败的错误且返回时 entry 为 Failed；后一次恢复返回成功且返回时 entry 为 Active
