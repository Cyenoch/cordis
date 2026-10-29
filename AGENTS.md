# Cordis Agent Instructions

## 项目边界

- Cordis 是 MIT 的独立 Go 插件框架，module 为 `github.com/Cyenoch/cordis`。
- 通用库、SDK、工具和验收只覆盖插件组合与 native 执行；应用服务由应用实现。
- `third_party/cordis` 是只读参考 submodule，固定提交 `35fdc969e4b6c48c48ed44717c7a018324e4dd88`，不得修改其源码。
- 原生插件具有用户权限，不是沙箱；资源清理不是业务事务回滚。

## 开发

- 不创建分支，除非用户明确要求。
- 行为、设计与任务的唯一事实源为 `openspec/changes/`；实现前阅读目标 change。
- 需求变化先更新规格，再更新设计和任务；README 只作入口，ROADMAP 只定义阶段顺序。
- 项目尚未发布，公开契约与 schema 直接同步修改，不保留历史字段、兼容包装或迁移分支；公开契约经独立审查。
- 复用已有设施，采用最小完整实现；只在有具体失败模式时增加防御，错误带上下文并显式返回。
- 测试保护可观测行为；通用验收入口为 `bash conformance/run.sh`，Windows 为 `conformance/run.ps1`。
- 项目文档使用中文，OpenSpec 结构标题和 SHALL/MUST 保持英文；只报告实际验证结果与平台。

## 协作

- Integrator 统一修改公开契约、go.mod/go.sum、schema 与 OpenSpec；其他 Agent 只改分配的写集。
- 并行修改使用独立 worktree，不覆盖用户或其他 Agent 的改动；关键边界由非作者 Review。
- 默认同一仓库存在并行工作：先读当前内容，按职责协调并合并增量，保留他人改动，继续完成自己的任务；发现其他改动本身无需询问。仅在实际冲突无法安全合并或存在覆盖、丢失工作风险时请求确认。
- Go 和 OpenSpec 的通用 skills 位于 `.agents/skills/`，保留各自来源与许可。
