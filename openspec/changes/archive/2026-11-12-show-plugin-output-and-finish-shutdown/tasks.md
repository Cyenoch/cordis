## 1. 实现

- [x] 1.1 CLI 转发插件 stderr：`run`、`dev` 默认，`host` 用 `-plugin-stderr`
- [x] 1.2 默认关闭期限 25 秒；未完成的清理报告错误，`run`、`dev` 保留目录
- [x] 1.3 `examples/basic-host` 同步
- [x] 1.4 测试：调用中 panic 的文本出现在 `run` 的 stderr（`host` 默认关闭由 flag 零值保证，不另设测试）

## 2. 文档与验证

- [x] 2.1 `docs/cli.md`、`docs/troubleshooting.md`、`CHANGELOG.md`
- [x] 2.2 独立审查
