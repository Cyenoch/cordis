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
`cordis host` SHALL 独占指定目录、按当前信任显式恢复已启用 entry，并在前台保持运行直到取消；不后台脱离或开放控制端口。逐实例 Waiting/Failed SHALL 可诊断，不能报告全部就绪，也不自动重试。宿主启动本身失败时非零退出；信号退出使用独立清理期限，清理不完整须报告，不等同于业务回滚。

#### Scenario: 前台恢复多实例
- **WHEN** 目录有两个启用 entry，其中一个缺少 provider
- **THEN** 可运行实例正常发布，另一个保持 Waiting 并输出原因；进程持续运行，不因短启动等待到期谎报整体 Active

#### Scenario: 中断与重开
- **WHEN** 用户中断前台运行并再次执行 host
- **THEN** 第一次调用等待关闭与进程回收，保留启用意图；第二次显式恢复并产生新 activation，不重放业务请求

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
