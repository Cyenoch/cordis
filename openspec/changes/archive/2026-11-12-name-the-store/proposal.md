## Why

`-store` 写错时，status、list 和 host 静默创建一个空目录并照常执行：status 报告没有 entry，host 什么也不恢复，看起来都成功了。store 被正在运行的 Host 占用时，错误只有 `storage already locked`，既不说是哪个目录，也不说该怎么做。

## What Changes

- 除 `install` 外的 store 命令（含 `host`）要求目录已存在，否则失败且不创建目录。
- store 被占用时，错误带 store 路径；CLI 提示 Host 运行期间用 `cordis control`。新增 `packagehost.ErrInUse`。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `framework-cli`: 不存在的 store 目录被拒绝；占用错误指向本机控制。

## Impact

- `packagehost`、`cmd/cordis`、`docs/cli.md`、`docs/troubleshooting.md`。
