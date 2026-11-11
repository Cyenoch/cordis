# 参与开发

先阅读 [AGENTS.md](AGENTS.md) 和目标 [OpenSpec change](openspec/changes/)。
共享 Go/OpenSpec skills 的来源保留在 `.agents/skills/` 与 `skills-lock.json`。

使用 go.mod 指定的 Go 工具链；文档工作流使用 package.json 固定的 OpenSpec 版本。
提交前运行 `go vet -tags=integration ./...`、`go mod verify` 和 `bash conformance/run.sh`；
规格修改使用 `bun install --frozen-lockfile` 后运行 `bun run spec:validate`。

`docs/*.md` 只放插件作者和 Host 开发者需要的手册。站点本地开发、部署、源码同步和构建期变量的维护方法见[文档站维护](.github/DOCUMENTATION.md)，不进入发布站点。中文正文按 humanizer-zh 校订，保留技术条件。

作者示例是独立 module，其 replace 只指向当前检出，确保验收运行当前 SDK。
教程使用本地 replace，与仓库当前代码一致；兼容性承诺见 `docs/stability.md`。
保留依赖许可证，不提交密钥、缓存、测试二进制或用户数据；未运行的平台明确标注。

`.github/workflows/conformance.yml` 在 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 runner 上分别构建 native 并运行通用验收，另独立校验 portable schema 和 OpenSpec。配置 CI 不等于这些平台已经通过；实际运行证据仍写入 tasks。不要用交叉编译或单一宿主 race 声称五平台执行。

聚焦变更可先运行 `go test -race ./wire ./sdk ./hosting`，收尾必须运行通用入口。与时序有关的改动，用手动触发的 `Stress` 工作流在慢平台上重复运行相关测试（`gh workflow run stress.yml -f runner=macos-15-intel -f packages=… -f run=… -f count=…`）；多个包并行比单个包更容易暴露问题。打版本标签前，在两种 Linux 架构上重复运行进程树与运行时的集成测试。

集成测试会启动 `go` 子进程。测试长时间没有输出、而 `go` 子进程 CPU 为零时，先用 `ps` 和 `sample <pid>`（macOS）或 `/proc/<pid>/stack`（Linux）记下这些进程在等什么，再终止：这种停住在一台开发机上出现过两次，至今没有留下现场。基准用 `go test -run '^$' -bench . -benchmem ./wire`；性能结果不作缺少场景依据的数字门禁。
