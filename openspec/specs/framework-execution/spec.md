# framework-execution Specification

## Purpose

定义外部 WASM 与原生进程插件的可互操作执行契约，覆盖版本协商、初始化、双向服务调用、事件、取消、故障和 Go 开发工具，使跨运行时组合不会泄露宿主对象或依赖隐式语言行为。

## Requirements

### Requirement: 外部执行形式
框架 SHALL 实际执行 WASM 模块和目标平台的原生进程产物，并提供 Go 作者工具链。两种形式 SHALL 支持初始化、调用、事件、停止和多实例；不支持的目标或特性 MUST 在执行前报告，不得静默降级。

#### Scenario: 两种 Go 插件运行同一契约
- **WHEN** Go 编写的 WASM 和原生插件分别实现同一服务契约
- **THEN** 宿主可通过同一类型化客户端调用它们，并得到相同契约下的结果与错误

### Requirement: 会话协商
宿主和插件 SHALL 在业务调用前协商协议版本和必需特性，支持范围内选择最高共同 minor。会话 SHALL 绑定插件、实例、激活及产物摘要；插件自报身份不得覆盖宿主绑定，协商失败不得发布服务和贡献。当前通用会话的 initialize SHALL 同时携带必需的 frameBytes，插件 SHALL 在作者校验/启动前应用该预算，成功 registration SHALL 原值确认；不得以省略字段、另一个默认值或临时放宽接收上限绕过它。

#### Scenario: 无共同版本
- **WHEN** 插件的兼容范围与宿主不相交
- **THEN** 会话被关闭，错误包含要求与支持范围，业务处理器不被调用

#### Scenario: 必要预算没有确认
- **WHEN** initialize 缺少 frameBytes，或成功 registration 缺少它或确认值与宿主提议不相同
- **THEN** 明确拒绝并关闭会话，不发布服务/贡献，不继续使用旧默认值，也不再次尝试另一种协议

### Requirement: 有界且可验证的消息
会话 SHALL 检查消息结构、方向、关联身份、大小和请求数量上限；日志不得混入协议。未知必需特性、重复键、畸形或无法关联的响应 MUST 不进入业务处理器，协议破坏 SHALL 终止对应会话并撤销其激活。接收端读到的违规帧与发送端尚未写出的超限结果 SHALL 分别处理；普通响应在发送前发现尺寸超限时，SHALL 遵守调用级失败隔离要求，不将其直接视为接收协议已损坏。

#### Scenario: 插件输出超大或畸形消息
- **WHEN** 插件实际发送超过接收端会话限制的响应或不符合协议的帧
- **THEN** 对应会话失败，宿主和其他插件继续运行，诊断不回显整个原始消息

#### Scenario: 正常编码器在发送前拒绝结果
- **WHEN** 已初始化会话的普通 handler 返回有效 JSON，但完整响应编码后超过发送端限制且尚未写出任何字节
- **THEN** 在替代错误能够编码并交付的条件下，仅该调用收到失败；原结果不发送，会话及无关调用继续有效

### Requirement: 可取消调用
每次外部调用 SHALL 有 deadline、取消身份和有界在途数量。取消后 MUST 不接纳该调用的迟到结果为有效提交；运行时在宽限期内未停止时 SHALL 终止该执行实例，其他会话不受影响。

#### Scenario: 插件忽略取消
- **WHEN** 调用超过 deadline 且插件在宽限期内仍不退出
- **THEN** 宿主终止其 WASM 实例或原生进程会话，调用得到超时结果，相关注册被撤销

#### Scenario: 取消与响应竞态
- **WHEN** 调用取消已经生效后成功响应才到达
- **THEN** 响应被标记为迟到而不提交新状态，不能把已取消调用重新标为成功

### Requirement: WASM 资源隔离
WASM 插件 MUST 默认不能访问宿主文件、网络、进程、环境秘密或其他实例内存；只能使用显式提供且授权的接口。宿主 SHALL 限制实例内存、执行时间与输入输出，并支持在独立 worker 中承载执行。

#### Scenario: 无限循环与未授权文件访问
- **WHEN** WASM 插件尝试读取未开放的宿主路径，或进入无限循环
- **THEN** 文件内容不可得，超时执行可终止，宿主进程保持可用

### Requirement: 原生进程信任说明
原生插件 SHALL 作为独立进程执行，并由宿主显式允许；进程退出和普通 panic 不得终止宿主。框架 MUST 不把进程隔离、签名、清理环境或会话授权描述为操作系统权限沙箱。

#### Scenario: 原生插件崩溃
- **WHEN** 原生插件进程发生 panic 并退出
- **THEN** 宿主检测退出并撤销该激活，其他会话继续工作，诊断保留退出原因

### Requirement: 类型化远端服务
跨运行时服务 SHALL 使用显式声明的版本化数据契约和代理，经过作用域解析、方向校验与授权。框架 MUST NOT 自动传递 Go 指针、channel、数据库连接、任意 interface 或闭包；非可序列化契约 SHALL 在生成或注册时拒绝。

#### Scenario: 外部 provider 被替换
- **WHEN** 消费者通过代理调用的 provider 激活已经退役
- **THEN** 新调用被拒绝或等待新绑定，旧代理不能绕过退役状态访问新实例，消费者清理顺序符合组合契约

### Requirement: 调用重入可诊断
框架 SHALL 检测同步调用与串行执行占用形成的等待环，包括同链重入和不同根调用之间的环，并明确拒绝新增循环等待；不得以无限等待或偷偷并发执行实例回调绕过契约。普通非循环双向调用 SHALL 能在控制消息和取消仍可处理的情况下完成。

#### Scenario: A 等待 B 而 B 又等待 A
- **WHEN** A 的处理器同步调用 B，B 在同一调用链同步回调已占用的 A
- **THEN** 循环重入被明确拒绝，两个调用可结束，控制与停止请求不被阻塞

#### Scenario: 独立请求交叉等待
- **WHEN** A 和 B 分别处理独立根请求，并在持有各自串行执行位置时同步调用对方
- **THEN** 宿主拒绝构成环的新增等待并返回可归属错误，调用不必等待超时或杀进程才能结束

### Requirement: 事件交付语义
跨运行时事件 SHALL 保留其声明的作用域、顺序、deadline 与分发方式；失败或停止后的未完成投递 SHALL 可诊断。框架 MUST 不宣称隐式持久队列或 exactly-once，重放和持久确认由宿主显式提供。

#### Scenario: 投递期间插件退出
- **WHEN** 一个事件已发送但尚未确认时插件退出
- **THEN** 投递结果为失败或不确定，不自动以新激活身份重放造成重复业务操作

### Requirement: 执行失败与重试控制
初始化失败、进程失败及不可恢复的会话协议或传输失败 SHALL 保留明确原因并撤销对应激活。已交付的普通调用错误，包括发送前结果超限的替代错误，MUST NOT 仅因该错误撤销整个激活。框架 MUST 不隐式重试有副作用的调用。宿主可显式配置有限次重启及预算，预算耗尽 SHALL 停止重启并保持可诊断失败。

#### Scenario: 重复崩溃达到预算
- **WHEN** 插件的启动连续失败并耗尽宿主配置的重启预算
- **THEN** 不再自动启动，宿主可明确显示失败原因并由用户或策略重新启用

#### Scenario: 业务已执行但结果超限
- **WHEN** 一个调用已产生业务副作用，其返回结果在本地编码阶段超限，随后小错误正常交付
- **THEN** 该调用返回错误，框架不重放 handler、不撤销已发生的业务副作用，也不触发该 activation 的崩溃重启

### Requirement: Go SDK 与独立开发验证
框架 SHALL 提供 Go SDK、包校验与打包工具、两种运行形式的真实示例及一致性验证入口。作者 SHALL 能从独立 module 构建插件并在独立示例宿主加载，无需导入宿主内部包或手写协议帧。

#### Scenario: 外部作者完成加载闭环
- **WHEN** 作者按文档在独立 Go module 构建、打包并安装示例插件
- **THEN** 宿主可调用其服务、接收事件并停用插件，工具能定位 Manifest 或契约错误

### Requirement: 取消监督和背压保持有界正确性
未确认的取消 SHALL 保持受监督，不得因历史淘汰丢失超时终止资格。已取消父请求的合法在途子请求 SHALL 返回取消而非杀死健康会话；真正未知关联仍 MUST 拒绝。发送准入等待 SHALL 响应各自 deadline，且在取得准入后重查取消。

#### Scenario: 取消洪泛与在途子请求
- **WHEN** 未确认取消后发生多次已确认取消，或取消父请求时合法子请求正在传输
- **THEN** 前者仍在宽限期内失败，后者不执行业务且不破坏健康会话

### Requirement: 严格且可往返的数据契约
typed 边界 SHALL 拒绝非 nullable 的 null、定长数组长度不符、非安全原始整数及 nullable 内未知字段。匿名嵌入字段 SHALL 遵循实际 JSON 有效字段规则。生成 number SHALL 保留合法输入的数值表示，使成功解码的值可按自身 schema 再编码。Parallel 事件 MUST 尝试全部监听者并汇总错误，实例内仍串行。

#### Scenario: typed 无损往返
- **WHEN** typed API 收到 null、长度不符数组、嵌入对象或高精度 number
- **THEN** 非法值在业务执行前拒绝，合法值往返不丢失契约数据

#### Scenario: 手写 json.Number 不能绕过数值边界
- **WHEN** 公共数据 codec 编解码 json.Number，输入是字符串 token、不安全整数、非有限数或合法高精度小数
- **THEN** 前三者在边界拒绝，合法数值保留原始表示；不能把 JSON 字符串静默变成数值

### Requirement: 统一作者工具版本与子实例协议
CLI SHALL 可选择 definition、service major 与 minor 范围，并默认解析唯一已声明版本，不硬编码 1.0。作者子实例生命周期 SHALL 由统一会话原语表达，不另建控制协议。

#### Scenario: 调用非零 minor 服务
- **WHEN** 作者运行提供 1.1 服务的合法包
- **THEN** 工具可正确解析并调用该服务，版本歧义给出可操作错误

### Requirement: 单一作者声明与配置生成
作者 SHALL 可从一份声明生成配置类型、严格 codec、定义描述、类型化启动入口、服务客户端及服务端和包元数据，不手工重复定义身份、版本与依赖。仅有配置而无服务的定义 MUST 可生成。声明式配置约束 SHALL 可由宿主独立验证，不执行插件来发现元数据或等待启动后才发现无效配置。

#### Scenario: 无服务插件的类型化配置
- **WHEN** 作者为只注册贡献的插件声明配置并生成产物
- **THEN** 可直接在类型化启动函数中使用配置，宿主可在运行前验证；不需要虚构服务或维护第二份字段列表

### Requirement: 生成代码可组合且避免无意义转换
独立契约 SHALL 可共同生成到同一 Go 包，共享必要接口而不重复声明公共辅助类型；真实符号冲突 MUST 在覆盖输出前报告全部相关输入。普通叶子字段 SHALL 使用内建或共享数据类型，显式命名的约束根仍保留校验。可选与 nullable SHALL 保留缺失、null 和零值差异；标记联合 SHALL 提供自动填写正确 tag 的构造入口。

#### Scenario: 两个契约共同使用
- **WHEN** 同一 Go 包生成两个服务和一个配置，业务把响应中的普通字符串传给另一个请求
- **THEN** 代码直接编译，无 Caller/Registrar 重定义和仅因生成路径不同而必须写的叶子强制转换，父对象约束仍严格生效

#### Scenario: 联合及可选值往返
- **WHEN** 作者构造一个联合分支并分别编码缺失、显式 null 和零值字段
- **THEN** 不需手写 tag；分支、字段存在性和零值准确往返，错误分支或额外字段仍被拒绝

### Requirement: 类型化客户端复用显式消费者范围
生成客户端 SHALL 能绑定与实例声明相同的已验证消费者描述，不把其 Requirement 改为生成时的精确 minor，也不自动扩大范围。描述 SHALL 关联生成的方法契约身份；服务名、major 或契约引用/指纹不匹配 MUST 拒绝，不据此自动证明跨 minor 语义兼容。宿主 SHALL 提供绑定 realm 的公开调用入口，可直接用于同一生成客户端，并复用原路由、取消和授权。

#### Scenario: 兼容 provider 的不同 minor
- **WHEN** 同一消费者声明接受 1.1 至 1.3，使用 1.1 契约生成的客户端调用实际 provider 1.2
- **THEN** 在方法契约兼容时调用成功，客户端不暗中收窄到 1.1；超出显式范围的 provider 仍不可绑定

#### Scenario: 同名服务引用错误方法契约
- **WHEN** 作者把同名同 major 但方法契约不同的消费者描述传给生成客户端
- **THEN** 构造在调用前拒绝并指出契约身份不符，不只凭版本范围判断匹配

### Requirement: 共用作者能力与可测试启动
框架 SHALL 提供宿主调用、贡献、任务和子实例所需的小接口或类型化 helper，作者不必断言具体 SDK Client。builtin 和外部插件使用相同能力时 SHALL 遵守相同声明及授权；缺少能力 MUST 明确拒绝。公开测试工具 SHALL 支持配置、服务、能力调用和清理的业务测试，无需作者手写会话握手；真实协议和运行时验收仍须保留。

#### Scenario: 同一业务定义用于测试和外部运行
- **WHEN** 作者测试包含类型化配置、宿主调用与清理的定义，然后构建 Native 与 WASM
- **THEN** 可复用业务代码和生成描述，测试不导入 internal 或手写帧，不因换运行形式而绕过授权

### Requirement: 安全且完整的本地开发闭环
CLI SHALL 提供成功退出的全局及子命令帮助、可识别版本信息、最小初始化模板、生成和构建打包流程。文件输出 SHALL 先完整验证及格式化，再原子替换，失败保留原文件。运行 SHALL 默认采用有限且可配置的启动等待，并在 stderr 报告结构化等待原因；配置和调用数据 SHALL 可从文件读取，stdin 归属与冲突须明确，不隐式提升信任或改变 native 策略。

#### Scenario: 生成失败不损坏旧代码
- **WHEN** 作者以文件输出模式生成已有代码，声明冲突或写入失败
- **THEN** 旧文件仍完整，错误指出来源；成功输出已格式化，不要求 shell 重定向覆盖源文件

#### Scenario: 等待依赖时退出
- **WHEN** 本地 run 缺少 provider 并达到默认或作者设置的启动期限
- **THEN** 有限时间内失败，stderr 显示定义和实际等待约束，stdout 不混入日志；帮助成功退出，非法参数仍非零

### Requirement: SDK 标准进程入口
SDK SHALL 提供统一的普通进程入口，处理会话运行、信号、错误退出和协议 stdout 隔离，避免每个插件重复 main 样板。嵌入者仍 SHALL 能显式使用可返回错误的低层运行入口。标准入口 MUST 不隐式重启业务、不吞清理失败、不把原生权限描述为沙箱。

#### Scenario: 独立作者使用生成模板
- **WHEN** 作者从最小模板构建 Native 和 WASM 插件并触发初始化失败
- **THEN** 框架入口返回确定的失败退出，执行已登记清理，协议 stdout 不混入普通错误输出

### Requirement: Windows 非阻塞 stdin 有界读取
Windows WASI worker SHALL 在 stdin 可读积压超过单个 guest 目标缓冲区时，仅读取该缓冲区范围内的字节；剩余数据 MUST 留待后续读取，不得 trap、丢字节或因限制读取量把正常数据误报为 EOF。

#### Scenario: 小缓冲区读取大积压
- **WHEN** stdin 已积压大于 4096 字节的数据，而非阻塞 guest 以 4096 字节缓冲区反复读取
- **THEN** 每次返回不超过该容量，全部数据按序且完整可恢复，worker 不因宿主 slice 越界退出

### Requirement: Windows stdin 调度与权限保留
读取修复 SHALL 保留非阻塞 EAGAIN、poll/HANGUP、关闭 fd 拒绝及宿主终止行为；非 stdin 操作 MUST 继续受既有文件表约束，失败不扩大能力或泄露 trap 原因。

#### Scenario: 暂无数据后继续运行
- **WHEN** 非阻塞 stdin 尚无数据且 writer 仍打开
- **THEN** guest 通过 WASI ABI 观察到 AGAIN 错误（编号 6），不阻塞调度，也不会将内部错误编号误读为权限拒绝；之后可继续消费到达的数据

#### Scenario: 关闭与非法 guest 内存
- **WHEN** writer 关闭、guest 已关闭 stdin 或 guest 提供越界的读取描述
- **THEN** 分别按 EOF、坏 fd 或内存错误返回；非法描述不消费 stdin，关闭 fd 后不能继续读取宿主流

#### Scenario: 取消等待中的受管插件
- **WHEN** guest 等待 stdin 或正在运行，而宿主取消并关闭受管 worker
- **THEN** worker 按既有终止流程退出，不新增无限等待、后台读取者或业务重试

### Requirement: 完整编码帧的字节预算
消息尺寸 SHALL 以最终 JSON 编码后的完整信封 UTF-8 字节及结尾一个 LF 字节之和计算；等于上限可发送，超过上限 MUST 在入队和写出前拒绝。成功结果与错误响应 SHALL 使用同一计量规则。字符数、原始 payload 长度、记录条数或固定信封估计 MUST NOT 替代最终编码检查。

#### Scenario: 完整帧恰好到达上限
- **WHEN** 某完整响应连同 LF 为 N 字节且限制为 N，另一响应连同 LF 为 N+1 字节
- **THEN** 前者可正常发送，后者在写出任何该帧字节前被拒绝

#### Scenario: 转义导致 payload 估计失效
- **WHEN** 原始 JSON payload 的字节数小于限制，但转义、信封及 LF 使最终编码超限
- **THEN** 以最终帧判定超限，不发送局部 JSON，也不通过关闭转义或截断内容伪造合规结果

### Requirement: 普通响应超限只失败当前调用
已初始化会话中，除 initialize、health、shutdown、activation.publish、activation.retire 之外的普通请求，其有效响应在本地编码阶段超限时，框架 SHALL 放弃原响应并最多构造一次固定小错误。错误 SHALL 复用原 activation、请求 ID 和 method，只携带已有 `plugin_failed` code 及固定安全说明，不携带原 payload 或原错误消息。替代错误可编码并成功交付时，MUST 保留会话及该激活。此规则 SHALL 对两个调用方向一致生效。

#### Scenario: 插件返回超大成功结果
- **WHEN** 宿主向插件发起普通调用，插件结果超限，同时同会话存在另一个可正常完成的在途调用
- **THEN** 超限调用收到 `plugin_failed`；无关在途调用及后续健康检查成功，activation 身份保持不变，原超大结果没有任何字节写出

#### Scenario: 宿主反向结果超限
- **WHEN** 插件调用宿主能力或服务，宿主的普通结果在本地编码阶段超限
- **THEN** 插件收到同关联的小错误，后续正常双向调用仍成功，不因该结果关闭插件进程

#### Scenario: 错误说明本身过大
- **WHEN** 普通 handler 返回的编码错误响应因 message 长度超限
- **THEN** 返回固定的 `plugin_failed` 小错误，远端及默认诊断不包含原超大错误内容，不递归生成新的替代响应

### Requirement: 尺寸拒绝不掩盖其他发送故障
仅能确定在发送前发生的尺寸拒绝 SHALL 进入响应替换路径。handler 响应的无效 JSON、其他编码失败、连接写入错误、无进展写入、发送准入背压超时及替代错误仍超限 SHALL 显式终止对应会话。实现 MUST NOT 通过错误字符串或任意通用错误 code 推测可恢复性，MUST NOT 为维持连接而放宽帧限制、无限重试或无界排队。

#### Scenario: 小错误也无法装入限制
- **WHEN** 预算或关联信封使固定小错误仍超过帧限制
- **THEN** 会话显式失败，不改写原关联、不发送截断错误，也不留下永久等待的调用

#### Scenario: 交付阶段失败
- **WHEN** 替代错误的写入发生部分写入后报错，或者发送队列阻塞导致准入期限到期
- **THEN** 关闭会话并结束等待，不把传输失败改成某个调用已正常收到业务错误

#### Scenario: 本地请求超限
- **WHEN** 调用者构造的完整请求在入队前超过限制
- **THEN** 仅本地调用失败，远端 handler 不执行，后续正常调用仍成功；不将请求错误误套为响应发送故障

### Requirement: 取消与生命周期优先于响应替换
发送普通响应或替代错误前已观察到的请求取消 SHALL 产生 canceled/timeout 结果；调用方取消生效后 MUST NOT 接受迟到成功或尺寸失败覆盖本地取消。替代响应 SHALL 使用原有响应关联与取消确认规则。initialize、health、shutdown、activation.publish、activation.retire 及初始化前的反向请求不享有本 change 的会话存活承诺；其超限 SHALL 保持会话失败及对应清理/关闭语义。

#### Scenario: 结果超限与取消交错
- **WHEN** 调用取消发生在结果编码或替代错误交付期间，且对端随后交付该请求的有效终态响应
- **THEN** 调用者仍收到取消或超时；迟到响应仅完成已有取消监督，不提交 payload，不因该正常确认误杀会话

#### Scenario: 初始化注册表超限
- **WHEN** 初始化返回的注册表或错误响应超限
- **THEN** 初始化失败且服务/贡献不发布，会话关闭并触发已登记清理；汇总并报告清理失败，不能将依赖已断会话的远端释放宣称为成功，不将初始化当作普通调用恢复

#### Scenario: shutdown 响应超限
- **WHEN** shutdown 的成功或错误响应超限
- **THEN** 不伪造成功清理确认；会话按失败关闭，宿主仍通过既有有界停止流程回收进程并报告实际错误

#### Scenario: 发布或退役控制响应超限
- **WHEN** activation.publish 或 activation.retire 的响应超限
- **THEN** 会话失败且遵守当前单调生命周期与清理规则，不把控制失败当作可忽略的普通调用错误，也不重放发布使退役激活复活

### Requirement: 作者可依赖的消息预算说明
协议和作者文档 SHALL 说明完整帧计量、单条记录超限、分页只限制集合而非任意单项，以及业务执行与响应交付的区别。框架 MUST NOT 自动截断 JSON、丢弃记录、分页、分块或重放业务调用。宿主专有协议及双方不同预算仍 SHALL 明确标注为本 change 的保证范围之外。

#### Scenario: 一页仅有一条大记录
- **WHEN** 作者把分页大小设为 1，但该记录的完整响应仍超过帧预算
- **THEN** 仍得到显式失败；文档引导作者在业务契约中选择投影、摘要、独立读取或显式分块，不把缩小页条数描述为必然成功

#### Scenario: 尚未统一双方预算
- **WHEN** 仅完成本 change，尚未实施 `negotiate-session-frame-budgets`，发送方预算大于接收方预算
- **THEN** 收到实际违规帧时仍关闭会话；本地响应隔离不能被描述为双方预算已一致，最终整体交付须包含后续 change

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

### Requirement: 本地运行默认期限覆盖 WASM 冷启动
`cordis run` 的默认启动期限 SHALL 不短于 WASM runtime 的默认编译期限，使默认配置下合法的冷编译不会被本地运行提前判为启动失败。期限仍 MUST 有限、可通过 `-startup-timeout` 配置且必须为正。

#### Scenario: 较慢机器上首次运行 WASM 插件
- **WHEN** 作者未设置启动期限，在较慢机器上运行需要冷编译的 WASM 包
- **THEN** 冷编译与启动合计在默认期限内完成时调用成功，不因本地运行更短的默认值失败；缺少 provider 时仍在默认期限到达后有限失败

### Requirement: 在途准入只计有效请求
接收端 SHALL 只把尚未被对端取消的请求计入在途上限；对端已发送取消的请求在其 handler 返回前 MUST NOT 占用准入额度。真正超过在途上限的请求仍 MUST 被拒绝。

#### Scenario: 超时洪泛后的健康调用
- **WHEN** 宿主发出等于在途上限的调用，全部超时并发送取消，而插件 handler 仍在退出中，随后宿主发出一次健康检查
- **THEN** 健康检查成功，会话保持可用，插件不被判为协议违规

#### Scenario: 真实超额
- **WHEN** 对端在已有等于上限的有效请求时再发送一个新请求
- **THEN** 新请求被拒绝并报告超出在途上限

### Requirement: 取消后尚未退出的请求有独立资源上限
接收端 SHALL 独立约束已取消但 handler 尚未退出的请求，最多保留四倍在途上限；超限 MUST 显式关闭会话，不得允许原始 peer 通过反复 request/cancel 无限累积 handler。该预算 MUST NOT 挤占仍有效请求的普通准入名额。

#### Scenario: 原始 peer 取消洪泛
- **WHEN** 原始 peer 持续发送新 ID 的 request/cancel，而对应 handler 尚未完成退出，数量超过四倍在途上限
- **THEN** 会话被关闭，后续请求不进入 handler

### Requirement: v1 通用 Runtime 使用一致的在途预算
通用 v1 Runtime SHALL 使用 SDK 的 64 个在途请求预算；显式配置其他值 MUST 在创建进程前返回配置错误。自定义 SessionFactory MUST 保留其自有协议的预算策略。低层 wire endpoint 的非默认预算 SHALL 由两端调用者保证一致，不新增 v1 帧字段。

#### Scenario: 启动前拒绝不兼容预算
- **WHEN** 宿主为通用 Runtime 配置在途上限 8 并启动插件
- **THEN** 启动返回配置错误，不创建随后会被第 9 个并发请求误杀的会话

### Requirement: 无关实例清理互不占用全局槽位
一个 activation 的合作式清理等待 MUST NOT 占满全局固定槽位而阻止无关实例清理。框架 MUST NOT 声称能强制终止进程内不合作的 callback；Host.Close 仍须等待其拥有的资源清理。

#### Scenario: 八个清理阻塞时停用无关实例
- **WHEN** 八个 builtin 的清理均阻塞，另一个无关实例被 Stop
- **THEN** 无关实例的清理可以执行并完成
