# framework-composition Specification

## Purpose

定义 Go 插件的实例、服务依赖、事件与资源所有权，使配置更新、停用和父子实例释放具备一致且可等待的行为；应用策略由使用者实现。

## Requirements

### Requirement: 实例与激活所有权
框架 SHALL 区分定义、实例与激活；每次启动生成独立身份。一个 Host 默认共享服务注册空间，实例可为指定 service key 显式建立其子树继承的隔离边界，父子关系本身 MUST NOT 自动隔离。父子关系 SHALL 表达生命周期所有权，停止父实例先清理子实例。

#### Scenario: 独立实例与父子释放
- **WHEN** 同一定义创建两个父实例并各自拥有子实例，随后停用其中一个父实例
- **THEN** 只有该父实例及子树停止，另一实例继续可用

#### Scenario: 同名服务的两组组合
- **WHEN** 两个父实例各自隔离 db@1 并在各自子树提供它，同时不隔离公共日志服务
- **THEN** 两个 db@1 不冲突，消费者分别绑定本组数据库，仍能绑定公共日志服务

### Requirement: 依赖激活与重新绑定
实例 SHALL 只在全部服务依赖就绪后启动。服务解析 SHALL 对每个 key 使用最近显式隔离边界，没有边界则使用 Host 根空间；边界内无 provider 时不得回退到边界外，同一空间内重复 provider 报冲突。provider 退役时先停止消费者准入、取消工作并等待消费者清理，随后释放 provider；清理仍可调用原绑定。新 provider 就绪后仍启用的消费者 SHALL 重新激活。

#### Scenario: 依赖下线与恢复
- **WHEN** C 依赖 B、B 依赖 A，A 被停用后重新提供服务
- **THEN** C、B 先于 A 清理；恢复时使用新绑定，旧初始化结果不能覆盖新激活

#### Scenario: 隔离服务缺失
- **WHEN** 子树隔离了 db@1，但只有根空间提供 db@1
- **THEN** 子树消费者保持 Waiting，原因说明本作用域缺少 provider，不把根或兄弟子树的 provider 当成候选

#### Scenario: 隔离组独立退役
- **WHEN** 两组使用同名 db@1，第一组 provider 被更新
- **THEN** 第一组消费者按原绑定清理并重绑；第二组保持运行，不被错误退役

### Requirement: 统一配置更新
Update SHALL 是配置更新的唯一入口，同时支持失败后重新初始化；Host 另提供保持当前配置的显式 Retry，仅对当前静态 Failed 登记生效。配置先校验再提交；并发更新收敛到最新 revision。框架 SHALL 默认不自动重启失败实例，仅显式配置的 framework-recovery 策略可有限重试；任何路径 MUST NOT 重放业务调用。

#### Scenario: 无效配置与显式恢复
- **WHEN** 未配置自动恢复的运行实例收到非法配置，随后一次有效更新的初始化失败，再次提交有效配置
- **THEN** 非法配置保留原实例；初始化失败可观察，后一次 Update 可恢复且没有后台重试

#### Scenario: 保持配置人工重试
- **WHEN** 当前静态实例进入 Failed 且清理完成，宿主明确 Retry
- **THEN** 使用当前配置建立新 revision 和 activation，不修改配置意图；被替换的旧登记不得控制新实例

### Requirement: 作用域清理
插件 SHALL 使用 Scope.Defer 登记资源清理，按登记逆序执行一次。初始化失败、停用和父退役均触发清理；错误或 panic MUST NOT 阻止其余清理。取消等待不得假报清理完成。

#### Scenario: 部分清理失败
- **WHEN** 插件注册两个资源后启动失败，后注册的资源清理 panic
- **THEN** 两个资源均尝试清理，原始错误与清理错误均返回

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

### Requirement: 可观察控制
实例 SHALL 提供状态、等待、Update 和 Stop；Host 提供显式启用、停用、恢复、查找和关闭。查找及枚举 SHALL 包括动态子实例，区分持久/动态来源并提供完整父激活身份；动态子实例句柄支持现有控制但不产生持久意图。长等待不占全局管理准入。取消等待 SHALL 不撤销已提交的控制意图。Stop、停用、删除与替换 SHALL 只返回停止过程中发生的错误：停止一个已经 Failed、或已在为记录的失败清理的实例 MUST NOT 返回该失败，它经状态结果与运行历史可见。Waiting 状态 SHALL 带至少一个等待原因；没有可报告原因的启动窗口报 Starting。

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

### Requirement: 应用服务边界
框架 SHALL 只管理插件实例、服务、事件与生命周期资源。应用需要的授权、任务、存储、贡献和脱敏 MUST 通过自行实现的服务完成，不能依赖框架内置代理。

#### Scenario: 应用自带服务
- **WHEN** 应用提供一个业务服务并声明依赖它的插件
- **THEN** 插件通过标准服务调用使用它，业务策略与数据生命周期由应用控制

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

### Requirement: 关闭完成与清理错误分离
Host SHALL 提供可观察的关闭完成状态，只有激活排空且 Store 已释放后才完成；Close 返回的历史失败或清理错误与完成状态分别表达。超时只结束等待，不能当作完成。调用者 MUST 能在已经完成且有错误时继续处理下一次启动，同时保留错误，不强杀进程内插件或丢弃其仍占有的资源。

#### Scenario: 已排空但带错误
- **WHEN** 插件之前初始化失败，Host.Close 返回该失败，但已完成清理并释放 Store
- **THEN** 调用者可确认关闭完成并记录错误，允许启动下一开发候选，不因非空错误永久误判为仍占用资源

#### Scenario: 未排空的关闭超时
- **WHEN** 进程内插件的清理不返回，Close 等待超时
- **THEN** 完成状态仍未满足，后续调用可继续等待；不得宣称资源已释放或删除仍在用的目录

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
