# Tasks

## 1. 组合与应用边界

- [x] 1.1 删除 access 与 Host/SDK 应用层接口、realm 和专属会话适配，迁移受影响测试与 API 文档；Go 测试验证服务调用、依赖与初始化失败。
- [x] 1.2 收敛 Update、子定义和 Defer，删除重试、监督、级联及重复启动入口；通过配置更新、父子清理、SDK 生命周期测试并更新对应教程。

## 2. Native 工具链

- [x] 2.1 删除 WASI runtime/worker/依赖和构建入口，同步收敛未发布 schema 与 fixtures，不保留旧字段；包格式、CLI 和独立构建测试验证 native，明确拒绝不支持的包。
- [x] 2.2 更新插件示例、native 验收和运行说明；真实进程验证双实例、依赖更新及停止。

## 3. 项目与文档

- [x] 3.1 同步 README、当前文档、主规格、导航与项目说明，清理具体宿主引用；文档检查与生产构建通过。

## 4. 集成审查

- [x] 4.1 执行通用验收、race 与独立公开契约审查，修复有效发现，记录实际平台及兼容性影响。

## 验证记录

2026-10-29，本机为 macOS / darwin arm64，Go 1.27.3。

- `bash conformance/run.sh` 通过：integration/race 测试、独立 native 构建、签名包双实例、配置更新、C → B → A 清理与重新绑定、调用取消、关闭重开恢复、失败版本显式恢复。另验证真实 native 父先发布、子等待依赖、父退役等待子资源释放，以及 First 事件保留明确处理的零值结果。
- `go vet -tags=integration ./...`、`go mod verify` 通过。`go generate ./examples/pluginmeta` 重跑后生成文件内容不变。
- `bun run docs:examples` 通过：直接提取组合实操、生成服务和快速开始，使用独立 module 构建并运行。快速开始覆盖 init、build、test、pack、inspect 和 native 调用。
- `bun run docs:test`、`bun run docs:build` 通过：当前源码变量、链接、锚点、页面构建与搜索索引校验。`bun install --frozen-lockfile --ignore-scripts` 通过，依赖版本未变。
- 使用临时 Python venv 安装 CI 指定的 `jsonschema==4.26.0`，执行 `python conformance/validate-schemas.py` 通过：4 份 metaschema、70 个结构向量和固定签名字节/hash。该脚本不执行 code-level 向量或 Ed25519 验证；实际 Go codec、包签名与进程行为由 Go 验收覆盖。
- Windows amd64、Linux amd64/arm64、macOS amd64：逐包 `go test -c -tags=integration`、`go build ./...` 及两个独立作者 module 编译通过。这些目标只做编译，没有在本轮实际运行；Windows PowerShell 验收入口未在 Windows 执行。
- `openspec validate --all --strict` 通过。四份当前主规格与本 change 的新增契约一致；`framework-access` 的旧要求及 Purpose 整体退役，应用边界归入组合规格。
- 独立只读 Review 覆盖核心 pending revision、清理、子实例、Host、native 和包边界，发现同父重复子定义 ID 静默选第一项。已在创建时返回 Conflict，并加入“两份实现均不启动”的行为测试，race 验收通过；此小修由主 Agent 完成，未再次独立复审。Review 不覆盖文档站和验收命令，二者由上述实际构建与运行验证。
- 最终全文检查无具体宿主专名；历史归档中的外部宿主信息已匿名化。`git diff --check` 通过，只读 `third_party/cordis` 未改动。

本次直接删除未发布 API 和旧格式字段，不保留别名、旧格式解析或迁移层。包签名算法继续用于当前原生包完整性；上述实施验证阶段未提交、推送或部署。

## 归档记录

2026-10-29 按用户要求归档，任务 6/6 完成；主规格已同步，应用能力规格已退役。ROADMAP 与文档站产品说明的变更链接同时更新。代码验证沿用本文件记录，本轮归档复验 OpenSpec、差异格式和文档构建。
