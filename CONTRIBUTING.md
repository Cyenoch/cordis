# 参与开发

先阅读 [AGENTS.md](AGENTS.md) 和目标 [OpenSpec change](openspec/changes/)。
共享 Go/OpenSpec skills 的来源保留在 `.agents/skills/` 与 `skills-lock.json`。

使用 go.mod 指定的 Go 工具链；文档工作流使用 package.json 固定的 OpenSpec 版本。
提交前运行 `go vet ./...`、`go mod verify` 和 `bash conformance/run.sh`；
规格修改使用 `bun install --frozen-lockfile` 后运行 `bun run spec:validate`。

作者示例是独立 module，其 replace 只指向当前检出，确保验收运行当前 SDK。
外部消费使用已发布的固定版本，不复制示例 module 的开发 replace。
保留依赖许可证，不提交密钥、缓存、测试二进制或用户数据；未运行的平台明确标注。
