# framework-execution Specification

## Purpose

定义 native 插件进程的初始化、双向服务调用、事件和停止协议，以及 Go 作者启动入口；进程由用户选择运行，不承诺权限隔离。

## Requirements

### Requirement: Native 进程执行
框架 SHALL 仅执行匹配目标平台的 native 产物，每次激活独占进程。不支持的 runtime 或平台 MUST 在执行前失败。native 插件具有启动用户权限。

#### Scenario: 双实例原生插件
- **WHEN** 同一包启用两份不同配置实例并停用其中一份
- **THEN** 两份实例分别运行，停用其中一份不影响另一份

### Requirement: 标准会话与身份
所有插件 SHALL 使用标准会话，在业务调用前协商协议与 frameBytes，并绑定宿主分配的包、实例、激活和摘要。未知必需特性及预算不匹配 MUST 拒绝，不提供专属宿主协议适配。

#### Scenario: 初始化预算不一致
- **WHEN** 插件未确认宿主提出的 frameBytes
- **THEN** 会话失败，不发布服务或启动受管工作

### Requirement: 有界消息与取消
协议 SHALL 校验消息结构、方向、关联身份、大小与在途数量；日志不得混入 stdout。业务取消通知对端。发送前发现普通结果过大 SHALL 仅失败当前调用；收到畸形帧则终止对应会话。

#### Scenario: 单次结果过大
- **WHEN** 一个业务结果编码超过帧预算，随后发起正常调用
- **THEN** 前次调用得到错误，后次调用仍正常完成

### Requirement: 单一 SDK 入口
Go 作者 SHALL 使用返回错误的 sdk.Run 运行一个或多个定义，统一信号、标准输入输出和清理。内存协议测试使用显式 transport 入口；框架不替作者调用 os.Exit。

#### Scenario: 进程取消
- **WHEN** 插件运行期间父 context 取消或收到终止信号
- **THEN** 已登记资源完成清理，错误返回给 main 处理

### Requirement: 类型化契约
生成器 SHALL 从单一作者声明生成配置、服务和消费者范围；严格数据校验在输入边界完成。builtin、native 和子实例省略配置均规范化为空对象且仍需符合定义 schema。

#### Scenario: 配置类型不匹配
- **WHEN** 某定义收到只符合另一份定义的配置
- **THEN** 在停止旧实例或启动插件前返回字段错误
