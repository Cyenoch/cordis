# Tasks

两个包互不依赖，代码写集不重叠；因为都追加 `verification.md`，按第 1、2 组的顺序执行。每组开始前先读 design.md 决定 6 所列的官方测试文件，把相关用例归类写入 `verification.md`。

## 1. 定时辅助

写集：`timer` 包及其测试、`docs/timers.md`。

- [x] 1.1 实现 `AfterFunc`、`Every`、`Sleep`、`Ticks`。验证：规格“延时与周期回调”“可取消的等待与节拍序列”的全部场景各有通过的测试，全部使用 `testing/synctest`，在 `-race` 下通过。
- [x] 1.2 实现 `Throttle`（含 `NoTrailing`）与 `Debounce`。验证：规格“节流与防抖”的全部场景各有通过的测试。
- [x] 1.3 编写 `docs/timers.md` 和包的 `Example` 测试，说明延时的起算点、回调的并发约定和大量定时器的建议。验证：`go test -run Example ./timer/...` 通过。

## 2. 日志服务

写集：`logging` 包及其测试、`docs/logging.md`。

- [x] 2.1 实现日志插件、`Service`、`Name` 设置、`Get`、分发处理器、默认输出和 `AddHandler`。验证：规格“按调用方命名的日志器”“输出目标”的全部场景各有通过的测试。
- [x] 2.2 实现默认级别、按名称的级别、`Level` 设置，以及全部配置经 `OnUpdate` 的就地应用。验证：规格“级别过滤与就地调整”的全部场景各有通过的测试；更新 `Format` 与 `Buffer` 时依赖日志服务的插件的启动函数没有再次执行。
- [x] 2.3 实现最近记录的环形缓冲和基于 `InstanceChanged` 的生命周期记录。验证：规格“最近记录”“生命周期记录”的全部场景各有通过的测试。
- [x] 2.4 编写 `docs/logging.md` 和包的 `Example` 测试，说明输出目标及其错误的去向、级别的三个来源和 native 进程内的用法。验证：`go test -run Example ./logging/...` 通过。

## 3. 示例与对照

写集：两个包的 `example_test.go`、`docs/composition.md`、`docs/api.md`、`docs/kernel.md`、`README.md`、站点导航。

- [x] 3.1 为两个包提供可运行的 `Example` 测试，更新对照表中 logger 与 timer 的对应与差异，把两篇文档加入站点导航，并在 `docs/api.md`、`README.md`、`docs/kernel.md` 加入入口。验证：`go test -run Example ./timer/... ./logging/...` 通过；`bun run docs:build` 通过。

## 4. 集成验收

- [x] 4.1 核对 `verification.md`：`framework-logging` 与 `framework-timers` 的每个场景对应一个已通过的测试名。验证：对照表没有空行。
- [x] 4.2 由非作者独立审查两个包的导出 API 是否符合 Go 惯例。验证：结论与处理结果写入 `verification.md`，没有未处理的阻塞项。
- [ ] 4.3 运行 `go vet -tags=integration ./...`、`bash conformance/run.sh`、`bun run docs:build`、`openspec validate add-logging-and-timers --strict`，并在 CI 的五个平台实际运行。验证：`verification.md` 记录精确提交、本机平台输出和 CI 链接；未执行的平台明确写出。

## Workflow follow-up

- 同步 `framework-logging` 与 `framework-timers` 主规格后归档本 change。
