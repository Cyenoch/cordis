# framework-control Specification

## Purpose

让用户通过显式授权的本机连接查询运行中 Host 并控制已有实例，同时保持目录独占、包信任、实例身份和生命周期边界。控制入口不承担远程部署、业务调用或多用户权限管理。

## Requirements

### Requirement: 显式且仅本机的认证连接
Host SHALL 仅在显式指定 control-file 后开启本机回环控制端点，生成每会话随机凭据并写入仅当前用户可读取的连接文件。客户端 SHALL 显式指定此文件；地址、协议和会话身份必须验证，不跟随重定向或系统代理。每个请求都须认证，不将凭据写到日志、参数输出或普通状态中。不支持远程监听。

#### Scenario: 未启用或未授权
- **WHEN** Host 未开启控制，或请求缺少/携带错误凭据
- **THEN** 无控制入口或请求被拒绝，不返回实例信息，也不执行任何控制

#### Scenario: 文件保护
- **WHEN** Host 在 Linux、macOS 或 Windows 创建连接文件
- **THEN** 普通其他本机用户不能读取凭据；文件已存在或无法安全创建时启动控制失败，不覆盖原文件

#### Scenario: 恶意连接地址
- **WHEN** 连接文件指向非回环地址或服务返回重定向
- **THEN** 客户端拒绝，不把凭据发给其他服务器或代理

### Requirement: 实时实例查询
在线 status SHALL 由持有 Store 的 Host 返回，包含当前会话身份、online 标记、持久意图和实际实例状态，支持按 entry 查询及枚举动态子实例归属。客户端不得直接读 catalog 或把离线历史称为实时。离线 status 保持原有目录锁行为。

#### Scenario: 运行时查询
- **WHEN** Host 持有 Store，客户端执行在线 status
- **THEN** 可看到 Active、Waiting、Failed 和动态 child 的实际归属；另一个离线 status 仍拒绝占用目录

#### Scenario: 已停用 entry
- **WHEN** 已存在的持久 entry 被在线 disable 后查询
- **THEN** 返回停用意图且不声称有活动实例，不因运行登记被删除而丢掉该持久记录

### Requirement: 已有持久实例的在线控制
控制 SHALL 支持 enable、disable、stop、resume、retry，作用于已有持久 entry。enable 使用保存的完整配置并保持信任/父子校验；disable 持久停用子树；stop 临时停止实例树但保留意图；resume 恢复已完全停止的实例；retry 仅重新启动当前 Failed 实例。动态 child 首版只提供查询，其控制由已有宿主句柄及父实例管理。未知 entry 或不适用状态 SHALL 返回明确错误。

#### Scenario: 临时停止与持久停用
- **WHEN** 分别 stop 和 disable 一个父 entry，并重开 Host
- **THEN** stop 保留启用意图，重开可恢复；disable 保存整棵持久子树的停用意图，重开不运行

#### Scenario: 启用保存的配置
- **WHEN** 在线 enable 一个已有但停用的 entry
- **THEN** 按保存的 definition、config、parent、isolate 启用并报告实际状态，不创建默认配置替代记录

#### Scenario: 失败后人工恢复
- **WHEN** 已完全清理的 Failed 实例收到 retry
- **THEN** 使用当前配置创建新 activation；对 Active、Waiting、Starting、Stopped 或动态 child 的 retry 明确拒绝，不重放业务请求

### Requirement: 控制结果与取消
请求 SHALL 有有限大小与处理期限，复用 Host 的操作互斥、身份验证和错误语义。超过期限或客户端断开只取消等待，不承诺撤销已提交状态；客户端 MUST NOT 自动重发有副作用命令。响应 SHALL 区分已知拒绝、提交后错误和无法确认结果，并提示重新查询。慢控制不得阻塞无关状态查询。

#### Scenario: 操作已提交但响应丢失
- **WHEN** disable 已提交后客户端超时或连接断开
- **THEN** 客户端报告结果无法确认，后续 status 显示真实状态，不自动再次 disable 或声称已回滚

#### Scenario: 并发冲突和坏输入
- **WHEN** 两条控制操作争用同一子树，或请求含未知字段/超限内容
- **THEN** 冲突或输入错误被明确返回，不绕过 Host 预约机制，也不停止无关实例

#### Scenario: 停止成功但带历史错误
- **WHEN** stop 已提交并将实例置为 Stopped，却返回其历史失败错误，或 disable 在部分子项完成后报错
- **THEN** 不根据错误码断言未提交；缺少确定提交证据时报告结果不确定，用户可查询受影响整棵子树

### Requirement: 控制会话随 Host 结束
Host 关闭时 SHALL 拒绝新控制、取消并等待受管请求结束，随后执行已有 Host 关闭流程；只有本会话创建且未被替换的连接文件可以清除。Host 崩溃留下的文件不得被误认成新会话，启动不自动抢占或覆盖。

#### Scenario: 重开与旧凭据
- **WHEN** Host 结束后重开，旧连接信息被再次使用
- **THEN** 不能控制新会话；正常退出清除自己的文件，异常遗留要求用户显式处理

#### Scenario: 控制过程中停止宿主
- **WHEN** 控制正在等待插件清理时 Host 收到退出信号
- **THEN** 新请求不再被接受，已有等待被取消，Host 按独立关闭期限回收；未完成清理如实报告
