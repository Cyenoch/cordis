# Spec Delta

## Purpose

定义框架独立于 VortexGo 的交付与集成验收，使其他 Go 应用无需携带游戏、Native UI 或 Nexus 依赖即可使用框架，同时以 VortexGo 已有插件契约检验外部包、隔离执行和资源所有权机制是否足够。

## ADDED Requirements

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
