# framework-host-integration Specification

## Purpose

定义框架独立于 VortexGo 的交付与集成验收，使其他 Go 应用无需携带游戏、Native UI 或 Nexus 依赖即可使用框架，同时以 VortexGo 已有插件契约检验外部包、隔离执行和资源所有权机制是否足够。

## Requirements

### Requirement: 独立消费
框架 SHALL 以 Cordis 名称、`github.com/Cyenoch/cordis` 独立 Go module 和 MIT 许可证公开交付；公开 SDK、宿主库与工具 MUST 不依赖 VortexGo 根 module、MyGo、Nexus 或游戏领域类型。VortexGo SHALL 仅作为外部消费者和集成契约来源。

#### Scenario: 独立检出与外部消费
- **WHEN** 用户只检出 Cordis 仓库或从独立 Go module 下载公开版本
- **THEN** 通用库、SDK、工具和验收无需相邻 VortexGo 目录，宿主专属 Adapter 与领域验收由宿主仓库维护

#### Scenario: 非游戏应用使用框架
- **WHEN** 一个独立 Go module 的命令行宿主引入框架并加载外部插件
- **THEN** 它能使用服务、事件和停用行为，无需引入 VortexGo、UI 工具包或游戏配置

### Requirement: 通用宿主端到端验收
框架交付 SHALL 包含可运行的非领域宿主及独立构建的 WASM、原生外部插件。验收 MUST 覆盖归档安装、双实例、类型化调用、服务退役、取消、失败、版本切换和卸载，而不只覆盖 mock 或源码结构。

#### Scenario: 双运行时完整闭环
- **WHEN** 从独立插件 module 构建两个运行形式的包，并在示例宿主完成安装、启用、调用、替换和卸载
- **THEN** 宿主无需重编译，退出和错误状态与各项规格一致，重复运行不依赖上次残留状态

### Requirement: 宿主协议可适配
框架 SHALL 允许宿主适配自己的包元数据、领域命令、查询、事件和会话编码；通用控制协议不得强迫已有宿主改变公开 ABI。适配器 MUST 执行相同激活、授权、取消和消息限制，不得绕过内核生命周期。

#### Scenario: 保持 VortexGo NDJSON 契约
- **WHEN** 宿主适配 `define-vortexgo-plugin-platform` 所定义的 Manifest 和 NDJSON 消息
- **THEN** 外部插件仍使用原有 schema、方法和错误码，框架只转换内部操作，不要求插件接受新的领域字段

### Requirement: VortexGo 隔离执行场景
框架 SHALL 能在独立 worker 中承载 WASM，并通过宿主代理访问能力；其集成验收 MUST 保留 VortexGo 的默认拒绝、版本化领域数据和凭据隔离要求，不以签名原生进程替代沙箱插件。

#### Scenario: WASM 无权读取宿主秘密
- **WHEN** 集成夹具中的 WASM 插件尝试获取未授权文件、宿主环境秘密或原始认证令牌
- **THEN** 插件无法获得内容，正常授权的裁剪查询仍可完成，宿主与其他插件保持可用

### Requirement: VortexGo 贡献和事务所有权
框架 SHALL 支持宿主定义并校验声明式贡献，按激活发布和撤销；Native UI 渲染、可访问性、领域事务和恢复 MUST 留在宿主。通用框架不得引入 React、任意视图执行或直接游戏目录写入作为集成前提。

#### Scenario: 插件失败不影响宿主事务
- **WHEN** 集成插件发布贡献后崩溃，宿主已接管的业务任务仍在执行
- **THEN** 该插件贡献和后续权限被撤销，宿主拥有的任务继续按宿主事务策略完成或恢复，其他贡献不受影响

### Requirement: 兼容性来源与覆盖报告
VortexGo 验收 SHALL 指明使用的契约快照及覆盖项；宿主契约改变时 MUST 显式报告不兼容，而不是静默补默认值。框架不得把契约夹具通过等同于真实 MyGo UI、真实游戏或部署事务已通过验收。

#### Scenario: 宿主新增不兼容契约
- **WHEN** 验收快照出现框架适配器不支持的必需方法或字段
- **THEN** 验收失败并指出差异，原有宿主规格不被框架测试重写

#### Scenario: MVP 消费真实机制
- **WHEN** VortexGo MVP 使用框架公开服务、依赖、包与 WASM 执行能力交付游戏业务插件
- **THEN** 框架提供通用机制与契约覆盖报告，真实 App、UI 和游戏认证由宿主完成，不以静态注册或夹具通过报告 MVP 已完成

### Requirement: 跨平台发布验证
框架 SHALL 在 Windows、Linux、macOS 的公布目标上验证外部包安装、WASM 执行、原生进程调用与停止。仅交叉编译 MUST 不被声称为平台运行验证；未运行的目标 SHALL 明确标注。

#### Scenario: Windows 子进程超时停止
- **WHEN** 在公布的 Windows 目标运行原生插件超时及卸载场景
- **THEN** 受管进程树被终止，插件代码不因遗留句柄而无法按协议卸载，结果附实际平台运行记录

### Requirement: 可复现的持续验收与公开文档
仓库 SHALL 提交跨平台 CI，执行现有验收入口、规格及互操作校验；本地与 CI SHALL 使用同一命令。公开文档 MUST 只依赖外部消费者可导入的 API，并解释提交错误、恢复、子实例和性能测量边界。

#### Scenario: 外部消费者按指南开发
- **WHEN** 宿主与作者从独立 module 按指南构建和使用框架
- **THEN** 无需导入 internal 包，版本调用、子实例与恢复路径可编译运行

### Requirement: 可检索的中文文档站与自动发布
仓库 SHALL 从现有 `docs/` Markdown 构建静态中文文档站，提供快速开始、插件开发、宿主接入、CLI 和既有参考文档的导航与本地全文搜索。入门步骤 MUST 使用公开 API 和真实示例，区分原生权限、WASM 限制与开发期未签名许可，不改变 Go API、v1 schema 或签名字节。

文档站 SHALL 使用当前稳定的现代静态站点工具链，并提供有明确视觉体系的浅色与深色阅读界面、代码高亮/复制、键盘操作与移动端导航；不使用默认模板外观或依赖运行时服务器。

#### Scenario: 新用户运行首个插件
- **WHEN** 用户按快速开始从仓库构建 counter、打包并通过 CLI 调用
- **THEN** 文档给出所需工具链、可执行命令与可观察结果，用户可继续找到插件作者与宿主集成指南

#### Scenario: 项目子路径访问与检索
- **WHEN** 站点部署到 `https://cyenoch.github.io/cordis/`，用户直接打开指南或搜索中文及 API 名称
- **THEN** 页面、静态资源、内部导航与搜索使用 `/cordis/` 子路径，仓库源码与 OpenSpec 证据链接指向 GitHub，不产生站内死链

#### Scenario: 主分支自动发布与 PR 校验
- **WHEN** 文档及其构建输入推送到 `main`，或提交 pull request
- **THEN** GitHub Actions 使用锁文件安装并构建静态产物；仅主分支推送或主分支手动执行可通过最小 Pages/OIDC 权限部署，PR 只校验，失败不替换线上站点

### Requirement: 完整使用手册与可执行实操
中文文档 SHALL 覆盖安装与版本选择、作者与宿主、服务依赖及子实例、realm、事件与 middleware、能力与贡献、任务接管、持久存储及迁移、数据契约生成、包签名和升级、运行时及协议、故障排查和上线维护。公开 API SHALL 有按包及用途组织的导航，区分公共入口与内部实现。高级实操 MUST 提供完整程序、运行命令及可观察输出，并由同一校验入口实际执行；进程内示例不得被记为外部 Native/WASM 验证。

#### Scenario: 新作者跟随高级教程
- **WHEN** 用户从干净临时 module 运行子实例、事件、middleware、任务、namespace 和生成服务的完整程序
- **THEN** 示例只导入公开包，返回文档给定的结果，相关 CI 使用同一程序验证，而不是另写一份近似代码

#### Scenario: 中文正文校订
- **WHEN** 补全或修改中文手册
- **THEN** 按 humanizer-zh 删除空泛铺垫和重复，保留条件、否定、版本、确定程度、命令、代码与链接，不把未验证的结果改成承诺

### Requirement: 文档代码基线与同步反馈
每篇 `docs/*.md` SHALL 记录完整代码 commit 与关联源码路径，站点 SHALL 提供可点击的代码基线。核对记录 MUST 来自人工复核，不以构建时间、文档修改时间或自动填入 HEAD 代替。同步检查 SHALL 验证 commit 存在、属于当前历史、路径有效，并比较该基线与当前检出的相关源文件；本地检查 SHALL 包含未提交和新增文件。

#### Scenario: 相关代码改变
- **WHEN** 文档关联的路径相对其基线有差异
- **THEN** 检查逐页报告相关文件，CI 以非阻塞警告提示维护者复核；没有差异只表示关联文件一致，不表示行为已经测试

#### Scenario: 元数据或历史不可核对
- **WHEN** 缺失 commit、路径无效，或浅克隆没有基线对象
- **THEN** 检查显式失败并指出页面和原因，不静默标记同步；CI 检出完整历史，维护者复核后才更新基线

### Requirement: 构建期文档变量
文档中的安装版本和工具链说明 SHALL 在本地与 CI 构建时从唯一配置源生成，最终站点 MUST 仍为静态 HTML。Cordis 安装版本 SHALL 由该页人工代码基线通过 Go module 查询解析，Go 和 Runtime 依赖版本 SHALL 读取该基线的 go.mod。站点 CI SHALL 读取仓库工具链配置。历史验收数字、schema 标识和示例业务版本 MUST 不随变量自动改写。

#### Scenario: 依赖与工具链升级
- **WHEN** 维护者复核并更新页面代码基线，或更新站点的工具链配置后重新构建
- **THEN** 页面中的安装命令和版本说明自动使用对应值，无需逐页改版本字符串；人工代码基线不被构建过程改写

#### Scenario: 变量无法解析
- **WHEN** 变量名未知、必要配置缺失，或代码基线不能解析为可获取的 Go module 版本
- **THEN** 构建失败并指出原因，不退回 latest、编造 pseudo-version 或输出未展开的安装命令；GitHub 原始 Markdown 的读取方式另行说明

### Requirement: 开发者站点内容边界
发布站点 SHALL 面向插件作者和 Host 开发者。仓库文档贡献流程、构建配置、Pages 设置及代码基线维护工具说明 MUST 留在仓库贡献文档中，不进入站点路由、导航或搜索；开发者文章只显示 API 版本使用所需的代码基线。

#### Scenario: 查阅开发者手册
- **WHEN** 用户浏览、搜索或直接访问发布站点
- **THEN** 只获得插件与 Host 使用文档，不发布 site-maintenance 页面，也不从页尾导流到网站维护操作

### Requirement: 统一的严格配置文件入口
公开库与 CLI SHALL 复用同一 TOML、YAML、JSON 解析及校验入口，按明确格式或扩展名选择，不轮流试解析。错误 SHALL 含来源、字段路径及可获得的行列。解析 MUST 保留数值词法值并拒绝重复键、非法 UTF-8、尾随文档、非字符串对象键、YAML alias/merge/自定义 tag 及超限输入；不做类型猜测、隐式环境合并或文件包含。

#### Scenario: 精确小数及重复键
- **WHEN** 用户加载包含高精度小数或重复配置键的文件
- **THEN** 合法数值不经 float64 舍入，重复键被定位拒绝；三种格式使用同一目标配置契约验证，失败不提交期望配置

### Requirement: 文件格式能力诚实表达
TOML SHALL 只承载其能够表示的对象配置；显式 null 与非对象根不得用遗漏、空串或字符串 "null" 伪装。YAML 和 JSON SHALL 按声明保留 null 与非对象根。三种格式的原生值 MUST 仅映射到已支持的数据模型，日期时间等应写为显式字符串，非有限数拒绝；不承诺三种格式对所有数据无损互转或保留原注释。

#### Scenario: 配置需要显式 null
- **WHEN** nullable 字段必须在本次配置中表达为 null
- **THEN** 作者可选择 YAML 或 JSON；TOML 缺失字段和字符串 "null" 不被转换成 null，文件格式不满足根类型时得到明确建议

### Requirement: 标准编辑器配置体验
工具 SHALL 从同一受限契约导出标准 JSON Schema 2020-12，并提供 JSON、YAML 和 TOML 的现有编辑器关联方式，无需开发新编辑器插件。导出 SHALL 转换 nullable、闭合对象和标记联合，不把自有关键字原样当标准。无法等价表达的正则、字节及整数范围等 MUST 明示运行时权威并保守投影，不误称编辑器检查等同于宿主验证。

#### Scenario: 编辑器理解配置契约
- **WHEN** 作者导出某 definition 的配置 schema 并在现有 YAML/TOML 编辑器关联
- **THEN** 获得字段补全、说明和可表达的类型检查；导出是标准 schema，nullable 联合及未知字段规则正确，额外运行时约束有标注

### Requirement: 通用分发可显式组合
宿主 SHALL 能在保留标准能力、任务、子实例和生命周期分发的同时扩展明确命名的自定义方法。委托 MUST 显式选择标准 dispatcher，不把任意业务错误作为未处理并重试其他路由。完整替换会话适配仍 SHALL 可用，保持宿主专有 ABI，不隐式叠加通用 envelope 或第二次授权。

#### Scenario: 扩展一个方法而保留默认能力
- **WHEN** 宿主添加自定义 guest 方法，并显式委托其他通用方法给框架 dispatcher
- **THEN** 标准宿主能力、任务及子实例继续可用，方法最多执行一次；denied 或业务失败不会触发备用路由

### Requirement: 无兼容胶水的统一开发者交付
本轮未稳定发布契约 SHALL 直接修订，同步公开 API、schema、生成器、SDK、CLI、示例和互操作向量；不为已移除字段保留别名、双版本分支或降级。开发指南 SHALL 从推荐格式走完生成、业务测试、构建、打包及加载，并说明版本、配置和持久意图的维护职责。历史验证 MUST 与新增工作的真实验收分开记录。

#### Scenario: 独立宿主和作者消费同一修订
- **WHEN** 两个独立 module 按本轮指南使用生成配置及客户端完成 Native/WASM 闭环
- **THEN** 不写手工 realm 适配器、不重复维护 Manifest 定义，不依赖旧字段或 internal；报告仅包含实际运行的平台与步骤

### Requirement: YAML 数字识别不接受双重符号
YAML 纯量 SHALL 只在至多一个前导符号时被识别为数字；带两个前导符号的纯量 MUST 作为字符串保留，且两种符号顺序结果一致。

#### Scenario: 双重符号的进制数
- **WHEN** YAML 配置包含纯量 `+-0x10`、`-+0x10` 或 `+-0o7`
- **THEN** 三者都解析为字符串，不产生数字

### Requirement: 错误分类不依赖文案
contract.Error SHALL 支持 errors.Is 按相同 Code 匹配，并保留取消及超时与标准 context 错误的匹配。未知字段解码错误 SHALL 提供可供 errors.As 读取的字段名，配置定位 MUST 使用该类型信息而非解析错误字符串。

#### Scenario: 未知字段带定位信息
- **WHEN** 配置解码因未知字段失败，错误又被包装
- **THEN** 调用方仍能通过 errors.As 获取字段名，配置错误定位到该字段

### Requirement: 未发布 API 不保留兼容包装
错误投影 SHALL 只使用 access.PublicError 包函数；包选择预检 SHALL 直接使用 Store.CheckSelect(ctx, ref, opt)，不保留旧签名或重复的 Context 后缀入口。没有调用方且无独立用途的 Limits、OpenWithLimits、TaskManager.Lookup MUST 移除；Policy.Calls SHALL 只返回查询结果。通用进程层 MUST NOT 硬编码宿主私有的 plugin.shutdown 方法名，自定义生命周期通过 typed PluginSession 映射。

#### Scenario: 单一预检入口响应取消
- **WHEN** 宿主通过 Store.CheckSelect 预检并取消 context
- **THEN** 操作停止校验且不改变选择，错误投影不需要 Sink 实例
