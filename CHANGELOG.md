# 变更记录

版本与承诺范围见 [稳定性](docs/stability.md)。

## v0.1.0

第一个带稳定性承诺的版本：会话协议 `cordis-go.session/v1`、包格式 `cordis-go.package/v1`、契约 `cordis-go.contract/v1` 从这里起只做向后兼容的修改。

- 进程内内核：实例、服务、事件、查询、中间件链、设置、隔离。
- Host 与 native 运行时：包的安装、选择、启用、替换、恢复；流式方法与跨进程中间件链。
- 用 Go 声明契约：`//cordis:service`、`//cordis:plugin`，`cordis generate` 推导 schema 并生成绑定。
- 配置文件驱动的插件树（`loader`），可编辑并写回 YAML。
- 日志与定时辅助。
- Host 选项：`Inflight`（会话的在途上限）、`Grace`（应答取消、关闭与退出的宽限）、`OnCall`（调用观测）。
