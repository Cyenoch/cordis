# Proposal

## Why

Conformance 自首个提交起在 ubuntu-24.04、macos-15、macos-15-intel 与 windows-2025 持续失败，只有 ubuntu-24.04-arm 通过。失败不是单纯的 CI 波动：其中一项是运行时缺陷，一项是与 runtime 预算不一致的 CLI 默认值，两项是测试自身错误。

- activation 拆除时先关闭 stderr 读端，再等待读取结束；进程退出前写入、尚未被读取的最后一段 stderr 被丢弃。慢机器上 `cleaned` 诊断因此缺失，本机以慢诊断 sink 可确定复现。
- `StderrBytes` 只触发终止，读取端在拆除期间继续把超限输出转发给宿主诊断出口，违背“限额采集”。
- stderr 回调返回错误后读取端停止 drain，持续写 stderr 的插件阻塞在满管道上，超限终止永远不会触发；回调永不返回时 `Close` 无限等待。
- `cordis run` 默认启动期限 5s，短于 WASM runtime 自身 30s 的默认编译期限；每次 run 都冷编译 guest，CI 与普通机器上 WASM 插件因此无法启动。
- Windows 构建测试把 go.mod 中写入的 `D:/...` 与 `D:\...` 比较；host DX 测试把工具链构建计入 30s 生命周期期限，且每次 WASM 激活都重新编译。

## What Changes

- 进程拆除在进程树回收后于 grace 内把 stderr 读到 EOF 再关闭管道；逃逸出进程树的写端最多拖延 grace，随后关闭读端。
- 转发给 `Launch.Stderr` 的字节数不超过 `StderrBytes`，超限部分继续读取但丢弃，终止语义不变。
- 回调失败后停止转发但继续 drain 并保留错误；回调在强制关闭读端后再超过 grace 仍未返回时，拆除不再等待并记录明确错误。
- `cordis run -startup-timeout` 默认值改为 30s，与 WASM 默认编译期限一致；仍有限、可配置且必须为正。
- 修正两个测试：按 go.mod 原文比较 replace 路径；host DX 测试的期限只覆盖生命周期，WASM 使用公开的 `CacheDirectory`。

非目标：持久化 CLI 编译缓存、改变 grace 范围、修改协议、schema、签名字节、go.mod/go.sum 或 Node 20 action 弃用警告。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-access`：stderr 采集在退出前完整、转发严格限额。
- `framework-execution`：本地 run 的默认启动期限覆盖 WASM 默认冷编译。

## Impact

写集为 `runtime/internal/process/process.go`、`cmd/cordis/run.go`，`runtime`、`cmd/cordis`、`packagehost` 与根包的相关集成测试及 fault fixture，`docs/cli.md`、`docs/getting-started.md` 和本 change。公开 Go API 与签名不变；CLI 默认等待变长，缺 provider 时默认在 30s 而非 5s 后失败，可用 `-startup-timeout` 缩短。下游 VortexGo 固定的版本不受影响，升级后才观察到上述行为。
