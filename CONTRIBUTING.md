# 参与开发

先阅读 [AGENTS.md](AGENTS.md) 和目标 [OpenSpec change](openspec/changes/)。
共享 Go/OpenSpec skills 的来源保留在 `.agents/skills/` 与 `skills-lock.json`。

使用 go.mod 指定的 Go 工具链；文档工作流使用 package.json 固定的 OpenSpec 版本。
提交前运行 `go vet ./...`、`go mod verify` 和 `bash conformance/run.sh`；
规格修改使用 `bun install --frozen-lockfile` 后运行 `bun run spec:validate`。

文档正文直接维护 `docs/*.md`，站点使用 Astro + Starlight。`bun run docs:dev` 本地开发；提交前执行 `bun run docs:test` 和 `bun run docs:build`，后者包含 Pages 子路径、链接与锚点检查。构建结果不提交，主分支由 Actions 自动发布；详见[文档站维护](docs/site-maintenance.md)。

作者示例是独立 module，其 replace 只指向当前检出，确保验收运行当前 SDK。
外部消费使用已发布的固定版本，不复制示例 module 的开发 replace。
保留依赖许可证，不提交密钥、缓存、测试二进制或用户数据；未运行的平台明确标注。

`.github/workflows/conformance.yml` 在 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 runner 上分别构建 native/WASI 并运行通用验收，另独立校验 portable schema 和 OpenSpec。配置 CI 不等于这些平台已经通过；实际运行证据仍写入 tasks。不要用交叉编译或单一宿主 race 声称五平台执行。

聚焦变更可先运行 `go test -race ./wire ./internal/core ./access ./sdk .`，收尾必须运行通用入口。typed 基准用 `go test -run '^$' -bench BenchmarkPublicTypedCall -benchmem .`；性能结果不作缺少场景依据的数字门禁。
