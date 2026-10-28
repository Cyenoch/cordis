# Design

## Context

消费者提供 Windows 11 ARM64 普通用户运行 amd64 worker 的 trap：可读积压 134413 字节，guest 缓冲区 capacity 4096，Wazero `internal/sysfs/file_windows.go:45` 执行 `buf[0:n]` 越界。消费者原始运行及堆栈仅作为外部故障证据，本仓库不复制用户数据或诊断路径。

2026-10-27 核对官方发布与 Go module 列表，最新发布仍为 [v1.12.0](https://github.com/wazero/wazero/releases/tag/v1.12.0)。[官方 main 固定提交 ef2dd2f85376c1a6ff79baae0cc1a5aa42c91db7](https://github.com/wazero/wazero/blob/ef2dd2f85376c1a6ff79baae0cc1a5aa42c91db7/internal/sysfs/file_windows.go) 仍有该未限长读取，没有可消费的现成修复。本地依赖 `go mod verify` 成功；没有修改模块缓存。

## Decisions

在现有 Windows `instantiateWASI` 接缝替换 `fd_read`，不改变模块配置中的 `WithStdin(os.Stdin)`，因此上游文件类型、非阻塞标志及 poll_oneoff 仍使用原文件。仅 named-pipe stdin 安装 override；普通文件/控制台完全保留上游路径。仅跟随原 `fd_fdstat_set_flags` / `fd_close` 成功结果维护该次实例 stdin 的 nonblock/closed 两个状态：初始为阻塞且打开，只有上游调用成功才更新。只有 stdin 非阻塞且未关闭时使用本地读取；阻塞 stdin、其他 fd 及其错误仍交给原函数。

不借用 guest memory 作 fdstat scratch，也不替换 fdstat 结果。Wazero 的 stdio 是 preopen，fd_renumber 对 from/to preopen 都返回 ENOTSUP；worker 不允许预开 FS/socket 或其他创建 fd 的宿主入口，因此没有重新打开 stdin 的第二条路径。状态仅属于一个 WASM 实例，WASI host 调用沿该实例串行执行，不跨 worker 共享。

本地 vectored 读取先完整验证描述表、所有非空 buffer、结果指针及描述/结果重叠，计算使用 uint64 防止乘法/地址溢出。PeekNamedPipe 必须先检查 Win32 成功状态，成功且无数据返回 EAGAIN，BROKEN_PIPE 返回 EOF，其他错误按 WASI errno 显式返回；读取长度为 `min(available, len(buffer))`。短读取后返回累计字节，后续 EAGAIN 不丢弃已读取结果。没有通用兜底、sleep 或额外 goroutine。

`experimental/sys.Errno` 与 WASI ABI 编号不同：例如前者 EAGAIN 为 2，而 WASI AGAIN 为 6。自定义 fd_read 的出口显式转换受支持错误到 WASI preview 1 编号；未知错误保持为 WASI IO（失败而非成功）。原 Wazero 函数已经执行转换，委托路径不能再转换。行为测试必须经过真实 guest 调用观察 WASI 返回值，不能把双方共享的错误类型数字当作 ABI 证据。

## Alternatives

- 上游升级：已核对没有可消费修复，不引入无关升级。
- fork/vendor 全依赖：仅一个 stdin 缺陷不值得引入完整依赖维护与许可证副本，且会触及共享 module。
- 普通 Reader wrapper：会丢失 *os.File 的非阻塞状态/轮询路径，容易阻塞 Go WASI 调度，不采用。
- 在消费者分块/延时：只是改变积压概率，未修边界越界，不采用。

## Validation

先建立独立 fd_read 回归，驱动真实 Windows pipe 及 guest memory，小缓冲区读取完整积压；同时验证无数据 EAGAIN、EOF、关闭 fd、flags、非法 iovec 不消费与现有 poll/取消。可在 macOS 执行有界 iovec 核心行为及 WASI guest/worker 集成；Windows 二进制交消费者主 Agent，在其现用 VM 串行执行。仅交叉编译不标 Windows 实跑通过。

独立 Review 后只提交本写集，发布后给准确 commit、校验值与命令，不将并行未提交开发纳入发布。消费者 module 升级和 Windows 业务验收由消费者 owner 完成。

## Planning Review

独立只读方案审查指出：Windows SetNonblock 成功并不代表普通文件/控制台具备非阻塞能力，Wazero 还要求 ModeNamedPipe；已将安装条件限定为 named pipe，并加入普通文件重定向回归。此处为方案静态审查，不是 Windows 执行证据。

## Intermediate Validation

消费者 owner 在 Windows 11 ARM64 普通用户下运行 amd64 最小 baseline，实际得到积压 21000 / capacity 4096 的原 readFd trap。初版 fixed 在积压与普通文件场景通过，但 flags 测试观察 28 而断言本地 EINVAL(7) 失败；owner 进一步确认自定义 fd_read 同样错误直传 local Errno，真实 guest 将 EAGAIN(2) 解读为 ACCES 而失活。初版未发布，产物撤回，不把部分测试通过视为修复完成。

已增加真实 guest 观察的 errno 回归：以原始直接 cast 的 overlay 运行时，AGAIN/FAULT/INVAL/IO/BADF 五项均实际失败；修复后的 ABI 转换再验证。Windows flags/memory/close 的后续步骤在首版失败时未执行，须用新产物重新验证。消费者的业务复现与 VM 始终由其 owner 独占执行。
