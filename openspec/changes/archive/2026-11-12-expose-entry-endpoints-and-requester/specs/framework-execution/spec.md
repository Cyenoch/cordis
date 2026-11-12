## ADDED Requirements

### Requirement: 宿主可见的请求方
Host SHALL 在代 native 激活处理的每个请求（调用、流、事件与查询、链、子实例操作）的 ctx 上提供发起请求的激活身份，包含包、entry、激活与摘要，内存运行时与 native 运行时一致。请求方 SHALL 是直接向 Host 发出该请求的激活：处理者用请求的 ctx 调用另一个 native 激活、后者再向 Host 发出请求时，后者是新请求的请求方。应用和进程内插件用不继承 native 请求 ctx 发起的调用 MUST 报告没有请求方。身份只是信息，框架 MUST NOT 据此授权或拒绝请求。

#### Scenario: 插件调用宿主服务
- **WHEN** native entry 一元调用和流式调用应用以进程内方式提供的服务
- **THEN** 两个处理者都从请求 ctx 读到该激活的身份，与 Host 报告的该 entry 状态中的身份相同

#### Scenario: 区分同一包的两个隔离 entry
- **WHEN** 同一包的两个隔离 entry 分别调用同一个宿主服务
- **THEN** 处理者看到的两个请求方 entry 与激活不同，包与摘要相同

#### Scenario: 嵌套请求的请求方
- **WHEN** 宿主处理 entry A 的请求时用该请求的 ctx 调用 entry B，B 再调用宿主服务
- **THEN** 宿主服务看到的请求方是 B，不是 A

#### Scenario: 宿主自己的调用
- **WHEN** 应用用不来自插件请求的 ctx 调用同一个服务
- **THEN** 处理者读到没有请求方

### Requirement: 请求关联与回调
会话请求 SHALL 可以用 `parent` 关联发出方正在处理的对端请求；被关联的请求继承那个请求的取消与期限，并沿用它在初始化与关闭期间的准入。对端 MUST 并发处理这类回调，不把 A 调 B、B 又回调 A 当作调用环拒绝。不带 `parent` 的请求独立处理。`parent` 指向已经回答的请求时，该请求以 `canceled` 结束，会话继续。

#### Scenario: 处理调用时回调调用方
- **WHEN** 宿主处理 native 插件 A 发来的调用时，用该调用的 ctx 调用 A 提供的另一个方法
- **THEN** A 在自己的调用得到回答之前处理这个回调，回调与原调用都成功完成

#### Scenario: 与请求无关的 ctx
- **WHEN** 宿主在同一处理中改用不来自该请求的 ctx 调用 A
- **THEN** 这个请求独立处理并成功完成

#### Scenario: 回调随原请求取消
- **WHEN** 回调仍在 A 中运行时，宿主取消最外层的调用
- **THEN** A 中的回调看到取消，最外层调用以取消结束，会话继续服务

#### Scenario: 已回答的 parent
- **WHEN** 宿主的请求已被回答之后，插件仍用处理那个请求时的 ctx 发出请求
- **THEN** 该请求以 canceled 结束，之后的调用照常成功
