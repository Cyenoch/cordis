# Proposal

## Why

需要一个独立、可复用的 Go 插件框架，既提供 Cordis 式动态组合与资源生命周期，也能在宿主发布后安装、加载和卸载外部插件包，而不重新编译宿主。VortexGo 是首个集成验收宿主，不是框架的领域模型、UI 或协议命名空间。

## What Changes

- 定义类型化服务、动态依赖、隔离作用域、事件与 middleware、可清理 effect 和可诊断激活状态。
- 把外部包发现、Manifest、完整性与信任校验、安装、启用、更新、停用和卸载纳入首个完整交付；静态注册不能替代外部包链路。
- 支持 Go 编写的 WASM 插件和原生独立进程插件；两者共用生命周期与会话契约，但明确区分沙箱能力和原生进程权限。
- 提供版本化调用、取消、跨运行时服务代理、Go SDK、示例和一致性工具，避免插件作者直接处理宿主内部类型。
- 提供 activation 所有权、能力撤销、贡献批量发布/撤销与结构化任务机制；实际文件事务、权限语义、UI schema 和持久化恢复由宿主拥有。
- 使用 Astro 7 与 Starlight 将现有中文 Markdown 发布为可检索的现代使用文档站，补齐入门、作者、宿主和 CLI 路径，通过 GitHub Actions 自动部署至 GitHub Pages；不引入文档后端或修改框架契约。
- 按 2026-10-27 用户确认，将既有框架迁至独立 Cordis 仓库 `github.com/Cyenoch/cordis` 并公开发布，采用 MIT。本 change 随代码迁移，仍为唯一规划源；VortexGo 专属 Adapter、ABI fixture 和验收归还宿主。

本次审计补齐作者子实例、候选集合发现、显式级联、运行中断记录、增量命名空间及跨平台 CI；修复取消监督、生命周期、严格 typed 数据和工具体验。当前未上线，不兼容旧插件；同步修改所有公开调用者，不添加 shim。已有 v1 schema 与签名字节不变。

兼容边界：借鉴 `third_party/cordis` 固定提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88` 的行为，不提供 TypeScript/Node/npm 兼容层，也不兼容 Cordis 或 Vortex 的现有插件二进制。上游源码事实不自动成为本框架规范；取舍见 design。

明确非目标：Go `.so plugin`、运行时编译任意 Go 源码、JS 引擎、插件市场、通用 UI 渲染器、透明远程 Go 对象、零停顿升级、任意外部 I/O 自动回滚、原生进程的跨平台权限沙箱。Cordis 的阶段顺序由 `ROADMAP.md` 定义；VortexGo 自行维护产品阶段依赖；Stardew MVP 必须实际消费本框架，宿主领域接入与真实游戏认证归相邻平台及 MVP change，不由通用 conformance 冒充完成。

## Capabilities

### New Capabilities

- `framework-composition`: 插件定义与多实例、动态服务依赖、作用域隔离、effect 清理、配置更新和类型化分发。
- `framework-packages`: 外部包格式、发现、校验、受管安装、版本切换、启停、卸载与中断恢复。
- `framework-execution`: WASM/原生执行、版本化会话、可取消调用、远端服务代理、Go SDK 与开发工具。
- `framework-access`: activation 绑定的能力检查、撤销、贡献与任务所有权、失败清理和诊断脱敏。
- `framework-host-integration`: 独立发布约束、非领域示例宿主和 VortexGo 无 ABI 变更的集成验收。

### Modified Capabilities

无。不修改 `define-vortexgo-plugin-platform` 中的 `plugin-runtime`、`plugin-capabilities`、`plugin-ui`，也不改变其 WASM worker、NDJSON 或领域 schema。

## Impact

- 本 change 拥有独立仓库内的框架实现与规划；VortexGo 仅通过公开 module 消费框架，其 ABI 不因拆仓改变。
- module path 为 `github.com/Cyenoch/cordis`，工具为 `cordis` / `cordis-worker`；不保留旧 import shim。已有 v1 schema 标识及签名域字节不随项目名称改变。
- 框架提供机制；宿主提供信任策略、公钥、能力具体语义、UI contribution 校验、业务存储和事务补偿。
- WASM 运行时、包签名、跨平台进程管理与协议代码属于后续新增依赖及安全审查面；选择、许可证与验证范围见 design。
- 实现与实际执行证据统一见 [tasks.md](tasks.md)。早期 Cordis/reactor 可行性实验保留为历史背景，不替代签名外部包、真实 WASI command、native 及跨平台端到端验收。
