# 变更记录

版本与承诺范围见 [稳定性](docs/stability.md)。

## 未发布

- `cordis pack` 先核对 `cordis_gen.go`、`cordis_gen.json` 与 Go 源码一致：改了契约没有重新生成时报错，不再打包旧契约。新增 `gocontract.Check`。
- 插件以带错误码的错误失败时，Host 报告的错误只带一次错误码（此前为 `invalid_message: invalid_message: …`）。
- `cordis run` 启动超时时若插件仍在 Starting，错误说明这一点并提示放宽 `-startup-timeout`。
- `plugintest.Start` 在 ctx 的期限内（没有期限时 30 秒）等不到所需服务时返回它在等什么，不再一直等到 `go test` 超时。
- 除 `install` 外，store 命令（含 `host`）要求 `-store` 目录已存在：写错路径时报错，不再静默创建并操作一个空 store。
- store 被另一个进程占用时，错误带上 store 路径；新增 `packagehost.ErrInUse`。CLI 提示 Host 运行期间用 `cordis control`。
- `pack`、`sign`、`install` 以及 `inspect` 的 `ref` 增加 `exact`（`id@version#digest`），可直接交给 `select`、`uninstall`。新增 `contract.PackageRef.String`。
- 生成的 endpoint 收到不存在的方法时列出可用方法的线上名称；从 Go 推导的契约在把流式方法当普通调用（或反过来）时直接说明。升级后 `cordis pack` 会要求先重新运行 `cordis generate`。
- 从 Go 推导契约时，本包里带 `json` 或 `cordis` 标签的未导出字段报错，不再被静默略去；约束不适用的报错改为 `minLength does not apply to integer values`。
- 配置不符合 schema 时，`cordis run` 与 `cordis enable` 的错误指出配置来自哪个文件（或 `-config`、stdin）；未知属性的错误列出可接受的属性名。

## v0.2.0

- **行为变化**：`hosting.Options.Grace` 的默认值从 1 秒改为 5 秒（`hosting.DefaultGrace`）。它是插件应答取消、响应关闭和退出进程的期限；1 秒在清理较慢或机器很忙时会把健康的插件当作卡住。需要原来的行为时设置 `Grace: time.Second`。
- `cordis generate` 先把两个生成文件都写好，再让它们各自就位：写入中途失败时保留原来的一对。

## v0.1.1

- 修复：Linux 上进程树在结束进程组之后、回收领头进程之前，等组内进程全部退出。v0.1.0 里这一步被跳过，取消构建或停止插件后，它的子进程可能还短暂存活。

## v0.1.0

第一个带稳定性承诺的版本：会话协议 `cordis-go.session/v1`、包格式 `cordis-go.package/v1`、契约 `cordis-go.contract/v1` 从这里起只做向后兼容的修改。

- 进程内内核：实例、服务、事件、查询、中间件链、设置、隔离。
- Host 与 native 运行时：包的安装、选择、启用、替换、恢复；流式方法与跨进程中间件链。
- 用 Go 声明契约：`//cordis:service`、`//cordis:plugin`，`cordis generate` 推导 schema 并生成绑定。
- 配置文件驱动的插件树（`loader`），可编辑并写回 YAML。
- 日志与定时辅助。
- Host 选项：`Inflight`（会话的在途上限）、`Grace`（应答取消、关闭与退出的宽限）、`OnCall`（调用观测）。
