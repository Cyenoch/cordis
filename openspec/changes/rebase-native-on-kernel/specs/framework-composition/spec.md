# Spec Delta

## ADDED Requirements

### Requirement: 进程内插件与 native entry 共用内核
Host SHALL 暴露其内核的根作用域，供应用直接加载进程内插件。进程内插件与 native entry 处于同一实例树和服务空间：任一方发布的可跨进程服务可被另一方依赖，双方遵守相同的依赖启动与停止顺序。Host 关闭 SHALL 同时停止两者。

#### Scenario: 进程内 provider 与 native 消费者
- **WHEN** 应用在根作用域加载一个发布某端点的进程内插件，再启用依赖该端点的 native entry，随后停止该进程内插件
- **THEN** native entry 启动并能调用该端点；进程内插件停止时 native entry 先完成清理并回到 Waiting

#### Scenario: native provider 与进程内消费者
- **WHEN** 应用加载一个依赖某端点的进程内插件，随后启用提供该端点的 native entry，再停用它
- **THEN** 进程内插件在 entry Active 后启动；entry 停用时进程内插件先完成清理，entry 的进程之后才结束

### Requirement: 持久子 entry 保持实例身份
持久子 entry SHALL 在父 Active 后启动，父停止或重启时先行完成清理。它 MUST 保持自己的实例身份、失败状态和恢复预算：父重启不为已失败的子 entry 创建新的激活，也不重置其重试计数。动态子实例随父的激活创建和结束，不保留身份。

#### Scenario: 健康的子 entry 随父重启
- **WHEN** 父 entry 因其依赖的 provider 变化而重启，它有一个 Active 的持久子 entry
- **THEN** 子 entry 先完成清理，父重新 Active 后子 entry 以新的激活启动；宿主持有的子 entry 句柄仍然有效

#### Scenario: 失败的子 entry 不因父重启而重试
- **WHEN** 持久子 entry 已 Failed 且恢复预算耗尽，父 entry 随后重启
- **THEN** 子 entry 保持 Failed，没有新的进程被启动，恢复状态中的已用次数不变

### Requirement: 进程内父实例
进程内插件 SHALL 可把自己的作用域登记为某个父标识；以该标识为父的持久 entry 在它 Active 后启动，在它停止或重启时先行清理。父未登记时这些 entry 保持 Waiting 并报告缺少父。同一父标识 MUST 只能被一个实例登记。Host MUST NOT 持久化进程内插件的代码或配置。

#### Scenario: 父重启后子 entry 重新启动
- **WHEN** 进程内插件登记为父标识 P，一个持久 entry 以 P 为父，随后该插件重启
- **THEN** 子 entry 先完成清理，再在该插件重新 Active 后启动；catalog 只记录父标识，没有该插件的代码或配置

#### Scenario: 父未登记
- **WHEN** Restore 时某持久 entry 的父标识还没有被任何进程内插件登记
- **THEN** 该 entry 保持 Waiting，等待原因指出缺少父；父登记后它自动启动

#### Scenario: 重复的父标识
- **WHEN** 两个进程内插件先后登记同一个父标识
- **THEN** 第二次登记返回冲突错误，第一个登记保持有效

## MODIFIED Requirements

### Requirement: 受管子定义
进程内插件 SHALL 用内核加载子插件。native 父 SHALL 可通过 SDK 创建同包定义的子实例，每个子实例独占进程并归属父的当前激活。子实例等待依赖不得阻塞父启动完成，已结束的父激活 MUST NOT 创建或控制新激活的子实例。请求包内不存在的定义 MUST 明确报错。

#### Scenario: 可选子功能
- **WHEN** 父启动期间创建缺少依赖的子实例，依赖随后到达，最终父停用
- **THEN** 父先进入 Active，子后启动并先于父完成清理

#### Scenario: 子定义声明冲突
- **WHEN** 父以同一个 ID 先后两次创建子实例
- **THEN** 第二次返回冲突，没有新进程被启动，已有的子实例不受影响

#### Scenario: 未知子定义
- **WHEN** native 父请求创建其包内不存在的定义
- **THEN** 返回错误，没有进程被启动，父保持 Active

### Requirement: 受管并发与事件
服务和事件回调 SHALL 可并发执行，native 实例的并发以会话在途上限为界，超出上限的调用等待名额而不是失败；显式受管工作在实例 Active 后启动，停止时取消并等待退出。事件支持顺序、并行和首个明确处理结果。订阅 SHALL 返回可取消句柄，支持启动期间及 Active 期间登记、once 和 prepend，自登记确认起生效。已取消或 once 已消费的句柄再次取消 SHALL 成功，活动名额释放不得使已准入投递丢失回调引用。服务隔离 MUST NOT 隐式改变事件的 Host 内广播范围。

#### Scenario: 退役时消费者清理
- **WHEN** provider 有常驻工作，消费者清理需要调用 provider
- **THEN** 消费者清理期间 provider 的常驻工作继续运行并完成该调用，之后才收到取消；非合作工作保持阻塞可见

#### Scenario: 同一实例的并发调用
- **WHEN** 同一 native 实例的服务同时收到两个调用，第一个处理函数等待第二个开始执行后才返回
- **THEN** 两个调用都成功完成

#### Scenario: 超过在途上限的并发调用
- **WHEN** 同时向同一 native 实例发起多于会话在途上限的调用
- **THEN** 全部调用最终成功；超出的调用在名额释放后才被发送，调用方的 context 取消时返回取消错误

#### Scenario: 动态订阅与取消
- **WHEN** 实例 Active 后登记监听者、收到事件，随后取消该订阅并再次发射
- **THEN** 后续发射不会新准入该监听者；已准入的回调可以完成，取消不自行等待它从而造成自取消死锁；实例停止仍等待全部准入回调

#### Scenario: once 并发准入
- **WHEN** 两次并发发射命中同一个 once 监听者
- **THEN** 最多一次进入监听函数；函数失败也不再次投递，未抢到准入的发射继续处理其他监听者

#### Scenario: 监听顺序与显式处理
- **WHEN** A 先登记，B 随后以 prepend 登记，按首个结果方式执行且 B 返回已处理和零值结果
- **THEN** B 先执行并终止本次执行，不能根据结果是否为零值决定是否继续；取消后重新登记按新登记次序排列
