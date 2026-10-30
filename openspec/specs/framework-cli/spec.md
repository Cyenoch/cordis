# framework-cli Specification

## Purpose

提供无需编写 Go 宿主即可完成本地签名包管理、显式前台运行和单包开发循环的命令行入口。离线意图与实时运行严格区分，复用库的信任、配置校验和生命周期，不建设另一套远程管理平台。

## Requirements

### Requirement: 离线包与实例意图管理
CLI SHALL 提供 list/install/select/enable/disable/remove/uninstall/status，显式选择 Store 目录；包选择和卸载使用完整包引用，不猜 latest。命令不启动插件；enable 保存或更新校验后的 entry 启用意图，对已有 entry 保留未显式指定的字段，清空须显式输入。disable 持久停用该 entry 及其持久子树，remove 只删除已停用且无持久子项的 entry 意图。status 输出持久意图和最近运行记录并明确当前不是实时状态。目录被占用时 SHALL 拒绝操作，不直接读写其内部文件。

#### Scenario: 启用不等于运行
- **WHEN** 在空闲目录执行 install、select、enable
- **THEN** 返回包引用和已保存的 entry；没有创建插件进程，输出不称 Active；随后 status 表明未连接运行中宿主

#### Scenario: 运行期间离线操作
- **WHEN** 前台 Host 持有目录锁，另一 CLI 尝试 status 或 disable
- **THEN** 命令非零退出并说明目录正在使用，不绕过锁或谎报停用成功

#### Scenario: 不兼容配置的版本切换
- **WHEN** 新旧版本配置 schema 不兼容，用户停用受影响子树、从叶到根 remove 旧意图，再选择新版本并重新 enable
- **THEN** 用户可在不写 Go 宿主、不清空 Store 的情况下使用新配置启动；省略 remove 时旧配置仍阻止 select，不静默丢弃配置

#### Scenario: 重新启用保留已有字段
- **WHEN** 已停用 entry 有配置、父项和隔离声明，用户 enable 时没有重新提供这些字段
- **THEN** 校验并保留原字段；只有显式传入空父项、空隔离列表或新 JSON 才分别替换对应值

### Requirement: 显式信任与可组合输出
install/select/enable 和前台 host SHALL 使用显式指定的信任文件，采用已有公钥、publisher/包 ID 授权、撤销语义；list/status/disable/remove/uninstall 不要求信任文件，但仍须取得独占目录。不得从包自签名信息推导信任。生产管理不支持全局跳过验签；run/dev 的开发许可只限本次精确摘要。命令结果输出 JSON 至 stdout，诊断写 stderr，沿用 0/1/2 退出码；CLI 不推断多层环境配置。

#### Scenario: 未授权签名
- **WHEN** 用户安装一个签名有效但签名者不在指定信任文件中的包
- **THEN** 安装失败，原有选中版本和 entry 不变；CLI 不自动导入该签名者

#### Scenario: 输出写入失败
- **WHEN** 已提交的离线 enable 在写 stdout 时失败
- **THEN** 命令报告输出失败及已提交事实，不把记录删除或自动重复 enable

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

### Requirement: 单包开发循环
`cordis dev` SHALL 对显式授权的一个本地作者目录执行监视、构建、打包、预检和重启，首版要求作者声明只有一个匹配当前平台的 native 目标，运行一个选定 definition 及其同包 children。监视包含模块源码、资源文件、作者声明、契约、go.mod/go.sum 和显式配置文件；工具生成文件、产物和临时目录不触发自循环。模块外输入只在用户显式增加监视目录时监视；不自动运行任意 hooks。

#### Scenario: 连续编辑
- **WHEN** 用户连续保存多个源码文件
- **THEN** 合并变更并串行构建；有更新输入时旧候选不得覆盖更新候选，输出对应构建与切换结果

#### Scenario: 首次构建失败
- **WHEN** 初始源码不能编译，用户随后修正文件
- **THEN** dev 保留监视并显示失败，不启动旧产物；输入改变后再构建，不在无变化时定时重试失败

#### Scenario: 多平台作者声明
- **WHEN** dev 输入同时声明本机和其他平台产物
- **THEN** 启动前明确拒绝并说明首版限制，不删改声明或伪造其他平台构建证据

### Requirement: 开发候选与失败边界
开发候选 SHALL 使用独立临时 Store 和精确摘要许可，完成构建、包完整性、平台、definition/config 预检后才停止旧 Host。相同发布版本的不同开发产物不得因此削弱正式 Store 规则。旧 Host 清理完成后才启动最新候选，沿用最新有效输入配置；初始化失败保持可见失败，不承诺恢复旧进程或内存。退出 SHALL 取消构建/监视、清理本次进程与临时目录。

#### Scenario: 编译或配置失败保留运行实例
- **WHEN** 新编辑不能编译或配置不符合候选 schema
- **THEN** 旧 activation 继续工作，候选失败可见；修正后可继续切换，作者源版本字段没有被工具改写

#### Scenario: 新代码初始化失败
- **WHEN** 候选通过预检，旧 Host 已关闭，但新插件初始化失败
- **THEN** dev 报告启动失败并等待后续编辑，不声称旧版本仍运行，不自动恢复代码或重放调用

#### Scenario: 修复失败候选后继续开发
- **WHEN** 用户修复上次初始化失败的代码，关闭失败 Host 返回历史错误但完成状态已经满足
- **THEN** dev 输出旧错误后仍能启动新候选；只有未完成清理才阻止切换

#### Scenario: 取消发生在切换期间
- **WHEN** 用户在构建或旧 Host 排空期间取消 dev
- **THEN** 不启动新候选；结束监视与构建，按真实结果报告清理，不删除仍被活跃进程使用的目录

### Requirement: 显式本地开发清单
dev SHALL 接受显式 project 文件，格式按扩展名选 JSON/TOML/YAML。清单列出本地作者目录、Go 构建包、持久 entry 定义/配置/父项/隔离及额外监视目录，相对路径基于清单目录。单包参数与 project 模式冲突时 SHALL 拒绝；未知字段、重复包或 entry、非法父子图与不匹配的本机目标 SHALL 明确失败。不自动获取远程插件或执行任意 hooks。

#### Scenario: 两个独立包共同开发
- **WHEN** 清单配置一个 provider 包和一个 consumer 包，并显式允许未签名开发
- **THEN** 两包实例在同一 Host 中建立服务依赖，保持各自配置和包身份

#### Scenario: 清单错误后修复
- **WHEN** 开发循环中的清单被改成非法内容，随后修正
- **THEN** 旧 Host 保留，错误可见；修正后重新预检，不需要结束 dev 进程

#### Scenario: 路径和模式歧义
- **WHEN** 从另一工作目录启动 project，或同时提供单包目录/配置 flags
- **THEN** 前者仍按清单目录解析路径，后者明确拒绝而不合并两种配置来源

### Requirement: 全组候选预检与切换
多包 dev SHALL 为每个 generation 创建隔离候选，先构建全部包并验证选中包依赖、所有实例配置与图，再停止旧 Host。必需包依赖必须由该清单满足，顺序不由文件排列决定。任何构建/打包/预检失败保留整个旧组；旧 Host 完成关闭后才启动最新候选，新组初始化失败如实显示，不回滚或恢复旧内存。

#### Scenario: 后一个包编译失败
- **WHEN** 第一包构建成功但第二包编译失败
- **THEN** 两个旧实例均继续运行；修复后再切换，不能先切换第一包

#### Scenario: 缺少包依赖
- **WHEN** 必需包缺失、版本不满足或包依赖成环
- **THEN** 候选预检失败并指出问题，不关闭旧 Host；只缺少服务 provider 时仍沿用 Waiting 语义

#### Scenario: 预检成功但初始化失败
- **WHEN** 新组通过全部预检，旧组关闭后其中一个实例初始化失败
- **THEN** 失败及其他实例状态可查询，等待下次输入变化，不自动恢复旧组

### Requirement: 全组输入代际与取消
多包 dev SHALL 监视清单、各包最近模块输入、显式配置和 watch 路径；生成代码、产物、内部工作目录与 Git 元数据不得引发自循环。任一输入变化产生全组 generation，构建与切换串行，过期候选不得启动。输入无变化时不反复重试失败；退出取消构建并回收全组进程，未完成清理的目录 SHALL 保留并报告。

#### Scenario: 构建中修改另一个包
- **WHEN** 正在构建 A 时用户修改 B，或旧组关闭期间再次修改清单
- **THEN** 过期候选被丢弃，最终启动涵盖最新输入的完整候选，不混用不同 generation 的产物

#### Scenario: 读取清单时并发修改配置
- **WHEN** project 解析后、输入扫描前，用户修改 inline config 或 definition
- **THEN** 不得用旧计划搭配新清单的 generation；最终生效的是最新配置

#### Scenario: 显式资源使用临时风格文件名
- **WHEN** 作者声明的普通资源名含 `.cordis-` 前缀或 `~` 后缀，用户修改其内容
- **THEN** 该显式输入仍触发重建，不因通用编辑器临时文件过滤而永久忽略

#### Scenario: 清单增删包
- **WHEN** 有效清单增加或删除本地包、entry 或配置文件
- **THEN** 新候选及监视集合跟随最新有效清单，不遗留旧实例；非法清单不丢掉修复它所需的监视

#### Scenario: 取消重建
- **WHEN** 构建编译器或插件清理尚未结束时用户取消
- **THEN** 不启动新组，构建后代进程被回收；关闭结果与保留目录真实可见

#### Scenario: 退出时仍有跨包后台调用
- **WHEN** consumer 的受管后台任务正在调用 provider，用户结束整个开发组
- **THEN** 插件先收到自身退役通知，再收到由退役导致的调用拒绝或取消；正常退出不被误报为后台任务崩溃，原请求主动取消仍可及时返回
