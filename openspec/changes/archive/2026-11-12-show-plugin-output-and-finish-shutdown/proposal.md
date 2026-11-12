## Why

插件在调用中 panic 或 `log.Fatal` 时，`cordis run`、`cordis dev` 只报 `read frame: EOF`：CLI 没有接收插件 stderr，panic 文本和栈都丢了，开发者无从定位。

CLI 的关闭期限默认 5 秒，等于插件宽限；一次 native 停止最多要用三个宽限（应答取消、收到信号后退出、排空输出）。插件慢关时 CLI 先退出，插件成了孤儿进程，`cordis-activation-*` 留在临时目录；`run` 还在 Host 未完成时删除自己的 Store，报 `directory not empty`。

## What Changes

- `run`、`dev` 把插件 stderr 转发到 CLI 的 stderr，每行带 `[entry] ` 前缀；`host` 只在给出 `-plugin-stderr` 时转发。
- `run`、`dev`、`host` 的默认关闭期限改为 25 秒（五个默认宽限：应答 shutdown、信号后退出、排空 stderr 至多两个，再留一个给终止与回收）。
- 期限到时清理仍未完成，命令说明清理未完成、插件进程可能仍在运行，并保留仍在使用的临时目录，不再删除它。
- `examples/basic-host` 采用同样的期限。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-cli`: 开发命令显示插件 stderr，host 显式开启；关闭期限覆盖一次完整的 native 停止，未完成的清理如实报告。

## Impact

- `cmd/cordis`、`examples/basic-host`、`docs/cli.md`、`docs/troubleshooting.md`、`CHANGELOG.md`。
