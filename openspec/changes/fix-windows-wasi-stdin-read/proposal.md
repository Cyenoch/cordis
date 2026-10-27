# Proposal

## Why

Windows 11 ARM64 上的 amd64 worker 消费真实 WASI 大请求时发生宿主 slice 越界，导致受管插件会话中断。消费者提供的 trap 定位到 Wazero v1.12.0 的 Windows 非阻塞 stdin：PeekNamedPipe 的可读总量直接用作单次目标缓冲区的长度。该失败不是业务 EOF，也不应靠调用者分块、重试或延时规避。

## What Changes

- 在 Cordis 的 Windows WASI 边界为非阻塞 stdin 提供有界读取，保留原始 `*os.File` 与既有 poll/HANGUP 路径。
- 空管道继续返回 EAGAIN，writer 关闭后返回 EOF；目标缓冲区、iovec 和结果指针在读取前验证。
- 阻塞读取、其他 fd 以及 fd 状态查询继续复用上游；不得绕过关闭状态或引入新的文件、环境、网络能力。
- 增加独立于消费者领域协议的读取回归和可交付 Windows 验证入口；保留 trap 脱敏。

非目标：改变帧预算、修改宿主业务协议、引入分块重试、新的 Wazero fork、通用 stdio 框架或并行控制消费者 VM。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `framework-execution`：Windows WASI stdin 在积压超过目标容量时仍有界、可轮询并保持 EOF/取消契约。

## Impact

写集仅限 `cmd/cordis-worker/` 和本 change；不修改消费者或正在开发的共享契约、SDK、go.mod/go.sum。Wazero 仍为 v1.12.0。原 Windows/Linux/macOS 证据不自动成为新修订通过记录，Windows ARM64 的 amd64 兼容进程不等于原生 Windows x64 认证。
