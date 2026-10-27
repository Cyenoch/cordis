# Spec Delta

## MODIFIED Requirements

### Requirement: 会话协商
宿主和插件 SHALL 在业务调用前协商协议版本和必需特性，支持范围内选择最高共同 minor。会话 SHALL 绑定插件、实例、激活及产物摘要；插件自报身份不得覆盖宿主绑定，协商失败不得发布服务和贡献。当前通用会话的 initialize SHALL 同时携带必需的 frameBytes，插件 SHALL 在作者校验/启动前应用该预算，成功 registration SHALL 原值确认；不得以省略字段、另一个默认值或临时放宽接收上限绕过它。

#### Scenario: 无共同版本
- **WHEN** 插件的兼容范围与宿主不相交
- **THEN** 会话被关闭，错误包含要求与支持范围，业务处理器不被调用

#### Scenario: 必要预算没有确认
- **WHEN** initialize 缺少 frameBytes，或成功 registration 缺少它或确认值与宿主提议不相同
- **THEN** 明确拒绝并关闭会话，不发布服务/贡献，不继续使用旧默认值，也不再次尝试另一种协议

## ADDED Requirements

### Requirement: 单一且显式的双向帧预算
通用会话 SHALL 由宿主显式指定一个 4096 至 1048576（含）之间的整数 frameBytes，并用于双向发送与接收。插件 SHALL 接受范围内的原值或显式失败，不另选更小或更大值。预算 SHALL 计入完整 JSON 信封、转义及 LF，整个 activation 内固定不变。无配置、零值、负数、非整数和越界值 MUST 拒绝，不代入默认值。

#### Scenario: 宿主收紧到 4 KiB
- **WHEN** 宿主指定 4096，插件返回一个大于 4096 且小于 1 MiB 的普通结果
- **THEN** 插件在发送原结果前生成同关联小错误，宿主仅该调用失败，后续双向调用和该 activation 继续有效

#### Scenario: 精确预算边界
- **WHEN** 预算为 N，完整编码帧加 LF 分别为 N 和 N+1
- **THEN** 前者能正常交付；后者在发送前被拒绝，普通响应按隔离规则失败，本地请求不执行远端 handler

#### Scenario: 预算非法或省略
- **WHEN** 通用宿主未指定预算，或 initialize 提供 null、字符串、零、4095、1048577 或未知预算字段
- **THEN** 在作者 Validate/Start 和业务副作用前失败；无初始化重试或缺字段默认

### Requirement: 初始化引导读取严格有界
Guest 仅第一条 initialize 帧 SHALL 使用 1048576 字节硬上限读取；严格解析出有效预算后 SHALL 用原始完整帧的实际字节数复核该预算。通过复核前 MUST 不调用作者代码、派生反向调用或读取第二帧。绑定后包括 registration、取消、初始化反向请求及生命周期控制在内的所有帧 SHALL 使用同一预算；Host 从首帧起就使用所选预算。

#### Scenario: 首帧自身超过提议预算
- **WHEN** 原始 initialize 为 6000 字节却声明 frameBytes=4096，即使重新编码去掉空白后小于 4096
- **THEN** Guest 拒绝并关闭会话，Start 调用数为零；不得用重新编码后的较小长度代替收到的真实字节数

#### Scenario: 超过引导硬上限
- **WHEN** 对端输出超过 1 MiB 仍无 LF 的首帧
- **THEN** Guest 在完整 JSON 解码前停止读取并失败，不等待更多字节或无界积累内存

#### Scenario: 首帧不是初始化或重复协商
- **WHEN** 未绑定预算时收到 health、业务请求或其他帧，或绑定后再次收到 initialize 试图改变预算
- **THEN** 明确拒绝并关闭会话，不执行 handler，不重置原激活或切换限额

### Requirement: 预算在初始化回调前生效
合法 initialize 的预算和关联身份 SHALL 在作者 Validate/Start、其反向调用、受管任务及后续输入准入之前生效。初始化确认成功仅表示预算和注册完成，不取代发布/退役生命周期。收到下一帧或已缓冲字节时 MUST 使用绑定后的预算。Guest 的成功确认可被对端观察之前，SHALL 原子提交其初始化准入状态；编码或入队失败 MUST NOT 留下已初始化状态。

#### Scenario: Start 内反向调用
- **WHEN** Start 通过合法 initialize parent 调用宿主，宿主返回超大结果
- **THEN** 双方都使用提议预算；该路径仍遵循初始化失败边界，不放宽预算完成启动，也不伪造注册或发布成功

#### Scenario: 同次写入携带初始化和后续帧
- **WHEN** 对端一次写入合法 initialize 和超过所提预算的下一条帧
- **THEN** 后一帧即使已落入读缓冲也以绑定预算拒绝，不能利用 readLoop 的旧限额被接纳

#### Scenario: Registration 后立即发送请求
- **WHEN** Guest 连续发送合法 registration 和初始化后允许的请求，Host 的 Initialize 调用者尚未被调度恢复
- **THEN** Host 在确认 registration 时先完成预算与协议状态校验，再接受下一帧；不得因调度次序误报未初始化，也不跳过独立发布门控

#### Scenario: Host 收到确认后立即回调
- **WHEN** Guest 的 registration 已被 Host 收到，Host 立即发回初始化后允许的请求，而 Guest 初始化 handler 尚未继续运行
- **THEN** Guest 已完成原子的初始化准入提交，不把合法请求作为初始化前请求关闭会话；独立发布门控仍有效

### Requirement: 关联边界保证固定错误可编码
当前通用协议 SHALL 对 activation、请求 ID、parent 和 method 施加一致的发送与接收限制：activation 为 1 至 128 UTF-8 字节，ID/parent 不超过 128 字节并满足既有方向序号规则，method 为 1 至 256 字节。合法关联的固定超限错误、canceled/timeout 确认与 cancel 帧 SHALL 在 4096 字节内编码完成；业务 payload 仍须独立检查。

#### Scenario: 最大关联字段和最坏转义
- **WHEN** 合法请求使用边界长度的关联字段和可导致最大转义扩展的字符串，业务结果超限
- **THEN** 同关联固定错误仍装入最低预算；原结果无字节写出，会话不因错误信封尺寸失败

#### Scenario: 关联值越界
- **WHEN** 本地或对端使用超过 128 字节的 activation 或超过 256 字节的 method
- **THEN** 本地在写出前拒绝，对端违规在 handler 前拒绝并终止会话；不截断 ID、method 或 activation

### Requirement: 初始化失败不留半启用会话
预算非法、registration 伪造确认、初始化输出超限、取消或传输失败 SHALL 进入现有初始化失败和有界清理流程，不发布服务与贡献，不开启发布后后台工作。清理失败 SHALL 汇总并报告，不将已断会话上的远端释放视作成功。框架 MUST 不为完成握手重试 Start、重发初始化或重启业务调用；若 Start 已有副作用，清理不等于业务回滚。

#### Scenario: 注册表超限
- **WHEN** Start 成功但完整 registration 超过预算
- **THEN** 初始化失败且不发布服务，触发本地资源/运行记录清理；清理失败和依赖已断会话的远端释放失败明确保留，不冒称全部回收成功，不截断注册表或临时提高上限

#### Scenario: 初始化取消和迟到确认
- **WHEN** Host 已取消 initialize 后 Guest 才返回确认
- **THEN** 不提交迟到注册，不发送 activation.publish 使其激活；在既有宽限监督下完成清理或终止进程

### Requirement: 当前契约单轨同步
当前 session 标识和 protocol major/minor SHALL 保持原值，所有通用 Host、SDK、Runtime、手工会话、schema 和 fixture SHALL 直接使用新必需预算契约。未知/缺必要字段 MUST 明确拒绝；不得保留旧字段别名、零值默认、超时降级、旧二进制兼容读取或版本探测重试。Host/Runtime SHALL 在启动前依据显式协议描述判定预算责任；自有协议由 Adapter 映射，不被注入通用帧。

#### Scenario: 直接 Runtime 与 SDK 入口
- **WHEN** 调用方不经过 Host 门面，直接启动 Native/WASI 或连接 SDK Transport
- **THEN** 仍显式提供并确认预算，不能因为走低层入口恢复隐式 1 MiB 或绕过输入校验

#### Scenario: 自有 SessionFactory
- **WHEN** 宿主显式提供自有协议 Adapter
- **THEN** 传输上不自动出现 generic initialize、frameBytes 字段或额外握手；Adapter 负责自己的预算与生命周期映射，generic 能力由显式接口而非失败回退识别

#### Scenario: 通用 wire 包装器配置无效
- **WHEN** 宿主提供显式标为 generic 的 SessionFactory，但帧预算无效
- **THEN** Host/Runtime 在启动进程前拒绝，不能仅因 factory 非空而豁免，也不能等返回 session 后才识别协议

### Requirement: 作者可查的有效预算
文档 SHALL 说明唯一配置来源、Initialize/Registration 确认、4 KiB 至 1 MiB 范围、完整帧计量、初始化失败与普通调用失败的区别。宿主配置和握手记录 SHALL 能确定该 activation 的预算，且不包含业务 payload。已知预算 MUST 不被描述为固定 payload 余量或允许运行中修改限额的承诺。

#### Scenario: 作者决定返回单条大记录
- **WHEN** 作者按宿主配置的预算把分页缩为一条，但转义和信封使结果仍超限
- **THEN** 最终编码检查仍拒绝，不自动截断；文档提供显式摘要/独立读取选择，并说明错误不代表先前业务副作用回滚
