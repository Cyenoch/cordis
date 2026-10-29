# Proposal

> 历史记录：外部宿主专名与路径已匿名化，原始内容可从 Git 查看。本记录不定义当前能力。

## Why

需要一个独立、可复用的 Go 插件框架，既提供 Cordis 式动态组合与资源生命周期，也能在宿主发布后安装、加载和卸载外部插件包，而不重新编译宿主。外部宿主 是首个集成验收宿主，不是框架的领域模型、UI 或协议命名空间。

本轮继续完善同一框架的开发体验：配置类型、宿主校验、服务声明和 Manifest 目前需要重复维护；部分公共调用还要求作者手写编解码、realm 适配或协议握手。已复现的恢复、准入等待、清理、数值和诊断问题也直接影响开发闭环，应由各自拥有机制的模块解决，而不是让每个插件补胶水。

## What Changes

- 定义类型化服务、动态依赖、隔离作用域、事件与 middleware、可清理 effect 和可诊断激活状态。
- 把外部包发现、Manifest、完整性与信任校验、安装、启用、更新、停用和卸载纳入首个完整交付；静态注册不能替代外部包链路。
- 支持 Go 编写的 WASM 插件和原生独立进程插件；两者共用生命周期与会话契约，但明确区分沙箱能力和原生进程权限。
- 提供版本化调用、取消、跨运行时服务代理、Go SDK、示例和一致性工具，避免插件作者直接处理宿主内部类型。
- 提供 activation 所有权、能力撤销、贡献批量发布/撤销与结构化任务机制；实际文件事务、权限语义、UI schema 和持久化恢复由宿主拥有。
- 使用 Astro 7 与 Starlight 将现有中文 Markdown 发布为可检索的现代使用文档站，补齐入门、作者、宿主和 CLI 路径，通过 GitHub Actions 自动部署至 GitHub Pages；不引入文档后端或修改框架契约。
- 补齐高级实操、公开 API 导航、排障及上线维护；中文正文按 humanizer-zh 校订，保留技术条件与限制。每篇文档记录核对过的代码 commit 和关联路径，构建显示基线，CI 报告相关代码变化，不自动更新核对记录。
- 按 2026-10-27 用户确认，将既有框架迁至独立 Cordis 仓库 `github.com/Cyenoch/cordis` 并公开发布，采用 MIT。本 change 随代码迁移，仍为唯一规划源；外部宿主 专属 Adapter、ABI fixture 和验收归还宿主。

此前审计补齐作者子实例、候选集合发现、显式级联、运行中断记录、增量命名空间及跨平台 CI；既有完成状态与证据保留在 tasks。本轮新增规划如下，尚未实现：

- 提供 TOML、YAML、JSON 的统一严格文件入口，推荐 TOML 书写对象配置；从同一声明生成类型化 Config/Start、服务代理、宿主前置校验和标准编辑器 schema，保留 null、数值及格式能力的真实边界。
- 简化作者包声明与版本维护：发行版本只写一次，服务 provider 用整数或 major.minor，消费者范围显式；格式、协议能力及配置 revision 由工具或框架负责，不要求逐插件手工同步。
- **BREAKING** 直接修订当前未稳定发布的 definition 配置、作者声明与生成器、公开调用接口及标准存储错误载荷；同步 schema、codec、所有调用者和向量，不提升 package/session/contract 兼容 major，不保留旧字段别名、回退、双版本读取或迁移 shim。签名域、输入布局及算法不因本轮需求变化。
- 补齐当前实例查找、逐条可重复恢复、短期管理准入、明确的临时停止/持久停用，以及一致的启动 context、受管工作和 panic 安全清理。
- 框架直接提供共用作者能力、标准进程入口、类型化 namespace 代理、当前授权撤销和普通终态任务回收；复用现有严格 codec、权限、任务和存储，不另建通用 Context 或持久工作引擎。
- 改善生成代码组合、消费者版本绑定、CLI 文件输出/帮助/等待诊断、业务测试及真实独立作者闭环；连接受信任诊断和可显式委托的标准 dispatcher，不改变宿主专有 ABI。

会话尺寸隔离与双方预算分别由 [isolate-oversized-responses](../2026-10-28-isolate-oversized-responses/proposal.md) 和 [negotiate-session-frame-budgets](../2026-10-28-negotiate-session-frame-budgets/proposal.md) 拥有；本 change 不重复定义预算、响应替换或另建协商版本。三者实施必须协调共享契约写集。

兼容边界：借鉴 `third_party/cordis` 固定提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88` 的行为，不提供 TypeScript/Node/npm 兼容层，也不兼容 Cordis 或 其他宿主 的现有插件二进制。上游源码事实不自动成为本框架规范；取舍见 design。

明确非目标：Go `.so plugin`、运行时编译任意 Go 源码、JS 引擎、插件市场、通用 UI 渲染器、透明远程 Go 对象、零停顿升级、任意外部 I/O 自动回滚、原生进程的跨平台权限沙箱。Cordis 的阶段顺序由 `ROADMAP.md` 定义；外部宿主 自行维护产品阶段依赖；Stardew MVP 必须实际消费本框架，宿主领域接入与真实游戏认证归相邻平台及 MVP change，不由通用 conformance 冒充完成。

本轮也不加入环境变量隐式叠加、include、热监听、注释回写、通用配置迁移、自动网络依赖解析、隐式信任或业务重试。schema-first 复用当前编译器，不并行引入另一套 Go tag、reflection schema 或编辑器插件。

## Capabilities

### New Capabilities

- `framework-composition`: 插件定义与多实例、动态服务依赖、作用域隔离、effect 清理、配置更新和类型化分发。
- `framework-packages`: 外部包格式、发现、校验、受管安装、版本切换、启停、卸载与中断恢复。
- `framework-execution`: WASM/原生执行、版本化会话、可取消调用、远端服务代理、Go SDK 与开发工具。
- `framework-access`: activation 绑定的能力检查、撤销、贡献与任务所有权、失败清理和诊断脱敏。
- `framework-host-integration`: 独立发布约束、非领域示例宿主和 外部宿主 无 ABI 变更的集成验收。

### Modified Capabilities

无。不修改 `[外部宿主插件规格]` 中的 `plugin-runtime`、`plugin-capabilities`、`plugin-ui`，也不改变其 WASM worker、NDJSON 或领域 schema。

## Impact

- 本 change 拥有独立仓库内的框架实现与规划；外部宿主 仅通过公开 module 消费框架，其 ABI 不因拆仓改变。
- module path 为 `github.com/Cyenoch/cordis`，工具为 `cordis` / `cordis-worker`；不保留旧 import shim。已有 v1 schema 标识及签名域字节不随项目名称改变。
- 框架提供机制；宿主提供信任策略、公钥、能力具体语义、UI contribution 校验、业务存储和事务补偿。
- 本轮实现涉及 `contract/`、`schema/`、统一文件配置入口、根 Host/core、SDK、access、Runtime 诊断、CLI、独立示例及 conformance；公开契约由 Integrator 统一修改并接受非作者审查。本轮只更新八份 OpenSpec 文件，不改实现、文档站或并行 change。
- 文档中的依赖与工具链版本在构建时从已核对的代码基线和仓库配置读取，输出静态内容，不逐页维护重复版本文本，也不自动提升人工代码基线。
- WASM 运行时、包签名、跨平台进程管理与协议代码属于后续新增依赖及安全审查面；选择、许可证与验证范围见 design。
- 实现与实际执行证据统一见 [tasks.md](tasks.md)。早期 Cordis/reactor 可行性实验保留为历史背景，不替代签名外部包、真实 WASI command、native 及跨平台端到端验收。
