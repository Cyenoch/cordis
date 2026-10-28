# Proposal

## Why

当前 Host 可收紧 wire 上限，但 SDK 固定使用自己的默认限额，Initialize/Registration 不传递预算；因此即使修好本地超大响应隔离，SDK 仍会把宿主接收不了的合法响应发上线路并令会话失败。应在尚未稳定发布时直接统一当前契约，让初始化回调和业务响应从开始就遵守同一预算。

## What Changes

- **BREAKING**：直接为当前 `contract.Initialize` 与 `contract.Registration` 增加必需的 `frameBytes`。宿主指定唯一预算，SDK 校验、应用后原值确认；不事后取双方默认值的最小值，不存在缺字段回退。
- **BREAKING**：当前通用协议支持 4096 至 1048576 字节的完整帧预算；1 MiB 是硬上限，不是省略配置的默认值。宿主显式配置，移除 Runtime/wire 中帧大小的另一配置来源和零值补默认规则。
- Guest 只对第一条 initialize 使用 1 MiB 的有界引导读取，严格解析后复核整条原始帧符合提议预算，并在作者 Validate/Start、初始化反向调用和下一帧读取之前绑定预算。Registration 及其前后的双向消息均用该值。
- 限制关联字段，使合法会话的固定超限错误和取消确认在最低预算内必定可编码；实际坏帧、错误关联、预算反悔和传输故障仍终止会话。
- SDK/Host/Runtime、SessionFactory 接缝、手工 wire 使用者、CLI/验收/示例、schema 和互操作向量一次同步。更新文档解释明确预算、初始化拒绝及完整帧边界。
- **BREAKING**：SessionFactory 直接改为携带协议种类与构造函数的描述符，启动前区分 generic wrapper 与自有 Adapter；只有明确的自有协议可不采用通用预算，不外包通用 envelope，也不把 generic wire 的保证冒用于自有协议。

版本政策：维持当前 `cordis-go.session/v1` 标识和 protocol major/minor，不增加 session/v2、新 major、feature 开关、旧字段别名、旧默认、旧二进制协商、兼容 shim 或双轨读取。用户最新的未发布契约政策覆盖早期冻结旧 v1 字段的约束；必要字段缺失、未知字段及非法值显式拒绝。签名算法及其输入布局无需改变。

非目标：协商 Inflight/deadline/Grace、流式分块、自动分页或重试、运行中调整预算、插件私有预算选项、通用内存配额系统及改造宿主领域协议。本设计来自本仓库的 SDK/Host 失败复现，不声称是上游 TypeScript Cordis 的协议要求。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-execution`：初始化前确立唯一双向完整帧预算、严格确认与拒绝、关联字段边界、公开入口和作者说明同步。

当前主规格目录为空，沿用 `build-go-plugin-framework/specs/framework-execution/spec.md` 的 capability。同步主规格顺序为 build 基线、`isolate-oversized-responses`、本 change；本次不改前置 change，不自行归档或另建同义 capability。

## Impact

- 依赖 `isolate-oversized-responses` 的唯一响应尺寸分类和小错误路径；两个 wire 写集串行集成。
- 核心边界：`contract/types.go`、`wire/`、`host.go`、`session.go`、`sdk/`、`runtime/{native,wasm,internal/process}/`，以及全部直接调用者、测试和 fake sessions。
- 契约证据：`schema/session.schema.json`、`schema/testdata/interoperability.json`、codec/wire/SDK/Host/真实 Native/WASI 回归及文档的代码基线元数据。
- 与并行配置/DX change 共享 contract/SDK/schema 文件，Integrator 合并双方增量，不重复定义包格式或诊断接口；本轮写集仅本 change。无需新增依赖或修改只读 submodule。
- 独立审查关注完整调用者/fixture 同步、初始化副作用前的校验、并发切换和不放宽接收策略；不要求兼容旧格式。
