---
codeCommit: 727f8f4e2c8df9429ec47b3ba0e04ec287ca276a
codePaths:
  - runtime
  - cmd/cordis-worker
  - contract/plugin.go
  - contract/types.go
  - go.mod
---

# 外部执行运行时

`runtime/native` 与 `runtime/wasm` 实现同一个 `contract.Runtime`，每次 `Start` 创建独立进程，不退回静态插件，也不重试业务请求。包目录须来自宿主已验证的不可变快照；运行时仍拒绝越过包根目录或不是常规文件的 artifact。

```go
nativeRuntime := native.New(native.Options{
    Lifetime: applicationContext,
    Grace: time.Second,
})
wasmRuntime := wasm.New("/absolute/path/cordis-worker", wasm.Options{
    Lifetime: applicationContext,
    MemoryPages: 4096, // 256 MiB，单位是 64 KiB
    CompileTimeout: 30 * time.Second,
    StartTimeout: 30 * time.Second,
    CacheDirectory: trustedCacheDirectory, // 可选；仅受信任宿主用户可写
})
session, err := wasmRuntime.Start(startContext, launch)
```

## API 与所有权

- `native.New(options ...native.Options) *native.Runtime`
- `wasm.New(workerPath string, options ...wasm.Options) *wasm.Runtime`
- 两者 `Start(ctx context.Context, launch contract.Launch) (contract.Session, error)`。
- `Start` 的 ctx **仅控制启动**；成功返回后取消初始化请求的 ctx 不会误杀激活。需要应用级联停止时显式设置 `Options.Lifetime`。这个 context 应覆盖应用生命周期，而不是单次初始化调用。
- 返回的 `Session` 独占激活。宿主调用 `Close`，或者 endpoint 协议失败/退出、Lifetime 取消、stderr 超额，都会关闭管道并终止进程树。`Done` 在 wait/reap、stderr 和管道清理、工作目录删除尝试结束后关闭；清理失败保留在 `Err`，不能把 Done 当作删除成功。
- `Close` 可并发、可重复，等待同一结果。启动 stdio 关闭后独立推进终止，避免 Windows 同步 pipe Read 阻塞 Close 而阻止 kill。Unix 向专用进程组发送 SIGTERM，Windows 以关闭输入为协作停止信号；宽限期后强杀并回收。Windows 等待 Job 的 ActiveProcesses 归零，再关闭 Job；执行映像延迟释放时仅对 ACCESS_DENIED/SHARING_VIOLATION 重试删除至多两秒，仍失败则报告。内核不可中断 I/O 不保证绝对墙钟期限。
- 默认 grace 是 1 秒，最大 30 秒；`Launch.Grace` 非零时覆盖 Options。正常显式停止引起的进程退出信号不算插件崩溃；协议错误、非主动退出及清理错误保留在 `Session.Err`。
- Runtime **不发送 initialize**。Facade 在 Start 后完成协议协商。`Launch.SessionFactory` 明确描述 generic/custom；nil 选择通用 wire，非 nil 的 Open 接收原始 transport、身份、handler 和帧预算。启动前完成描述符及通用预算校验，自有 ABI 不被注入额外 envelope。专有 session 的 Close 必须及时返回，宿主 handler 必须响应自己的 ctx；这里不假称能够杀死任意宿主 Go goroutine。通用 wire 的 `Wait` 会等待 host handler，不属于外部进程 reap 的完成条件。

专有构造器返回实现 `contract.PluginSession` 的 session 时，受管进程 wrapper 保留 typed Initialize/Publish/Retire/Invoke/Deliver/Shutdown。Facade 的 `Options.SessionFactory` 根据已验证 Manifest 选择构造器，`Options.HandleGuest` 仍在正常 Scope 和 policy 边界后执行，不能以自定义 envelope 绕过授权。专有协议的行为验收由宿主维护。

## Native：信任而不是权限沙箱

Native 用 `exec.Command` 启动精确路径，不调用 shell，不做 PATH 搜索；每个激活在权限 0700 的私有工作目录复制单一精确 artifact，并以 0700 创建可执行快照，因此包存储中的 artifact 可以保持 0600，其他包文件不会被复制或变成可执行。Windows 私有副本固定命名为 `plugin.exe`，不依赖包内 artifact 的扩展名；其他平台使用 `plugin`。进程的 cwd 是该私有目录，不是包目录或宿主 cwd。

默认环境为空。`native.Options.Environment` 是宿主显式选择的 `map[string]string` 白名单值，构造器会复制它；不要把 `os.Environ()` 整体放入。需要 Windows 的系统环境时，应由部署宿主按实际需求仅传入例如 SystemRoot 等必要项，不能顺手开放秘密、代理凭证或动态库注入变量。

Unix 专用进程组以及 Windows suspended-create → Job Object assign → resume，防止初始子进程在纳入管理之前执行。Job Object 设置 KILL_ON_JOB_CLOSE。停止时清理管理范围内后代；自行脱离进程组/daemonize、访问宿主用户可读文件、网络、其他进程等不属于权限沙箱承诺。宿主必须在信任策略中显式允许 native。

## WASM：独立 worker 与 WASI command

构建随宿主分发的可信 worker：

```sh
go build -o /absolute/path/cordis-worker ./cmd/cordis-worker
GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm ./your-plugin
```

`wasm.New` 的 worker 必须是绝对常规文件路径；`Launch.Worker` 可以明确覆盖它。worker 和 guest 不继承宿主环境。worker 每次只加载一个 WASI preview 1 command（必须有无参数、无结果的 `_start`），不是 reactor ABI。stdout 原样用于插件会话，stderr 仅用于日志。

wazero 固定 {{docs.wazeroVersion}}，启用 `WithCloseOnContextDone(true)`。默认模块文件最大 64 MiB、每实例线性内存最多 4096 pages（256 MiB），编译与到首次读取会话 stdin 的启动阶段各 30 秒。编译的进程内硬 watchdog 覆盖编译器不及时响应 ctx 的情况。启动达到首次 stdin 读取之后，command 是持续运行的消息泵，而不是把整个激活生存期误当成一次有期限的调用。

初始化/业务调用的 deadline 和最多在途数由 Runtime 的 `Options.Limits` 传给 wire，默认 30 秒 / 64。帧大小不再位于 Limits：Host 使用显式 `Options.FrameBytes`，直接 Runtime 使用 `Launch.FrameBytes`，范围为 4096–1048576，零值不补默认。SDK 在 Start 前应用预算并在 Registration 原值确认；详见[协议参考](protocol.md#帧预算与失败范围)。忽略取消且宽限期内无有效响应会关闭 endpoint；Runtime 接到 Done 后终止独立 worker。CPU 无限循环和阻塞 stdin 都不依赖 guest 合作。自有 SessionFactory 需明确标为 custom 并自行负责调用限制，不注入通用协议。

不 preopen 任何目录，不配置 FS、不向 guest 传入环境、不暴露任意 host module；只实例化 WASI，拒绝非 WASI import、imported memory 和 `sock_*` import。标准 WASI 文件调用没有预开放文件描述符，因此不能读取宿主任意路径。时钟和随机数显式提供；guest 的参数仅为固定程序名，不含宿主模块路径。worker 诊断不输出 guest 堆栈、源码或模块内容。

SDK 和通用 wire 使用标准 Go goroutine 的独立业务/控制读取，不提供要求作者手动驱动的 Cooperative/Pump API。worker 的 stdin 保持 `*os.File`，让 wazero 的非阻塞读取与 `poll_oneoff` 能推进 Go WASI 调度；不能用普通 Reader wrapper 丢失该能力。

Windows worker 修正 wazero v1.12.0 的 pipe EOF poll：仅在原 `poll_oneoff` 返回 WASI `EIO=29` 且 `PeekNamedPipe` 确认 BROKEN_PIPE 时，为 stdin 订阅返回保留 userdata 的 FD_READ/HANGUP 事件，让 Go poller 读取 EOF。调用原函数前拒绝 count 长度乘法溢出及 input/output/result 交叠；不复制不可信长度的 subscription 数组。其余返回值仍由原 WASI 实现处理；没有开放新文件描述符或路径权限。成功 SDK shutdown 在完整写出确认后关闭会话；宿主已匹配结果在会话锁内发布，预期进程退出先有界 drain stdout，避免 EOF 丢失确认。

`wasm.Options.CacheDirectory` 显式启用跨 worker 的持久编译缓存，worker 对应 `-cache-directory`。空字符串默认禁用，每个新 worker 重新编译；配置后使用 wazero `NewCompilationCacheWithDir` / `WithCompilationCache`，每个 worker 仍有独立 Runtime、实例和进程。目录必须仅受信任宿主用户可写，不应位于插件可写目录或被 guest preopen；缓存不是包信任来源，也不会授予 guest 文件权限。缓存不适用或失效时仍可能编译，没有逐次命中探针；首次传入已存在目录不能称为 cache miss。测量命令、空缓存与复用目录的区别见[性能文档](performance.md)。

## 日志、故障与验证

默认最多接收每激活 1 MiB stderr；只计数、持续 drain，不保留/回显日志内容。`Launch.Stderr` 收到的总量不超过该限额，超过限额使对应激活失败并清理。进程退出后在 grace 内继续读到 EOF，退出前写入的最后输出不会因拆除而丢失。Native panic 的退出原因保留为 OS exit error，但 stderr 中的秘密不会附到错误；WASM trap 仅给出脱敏错误。协议原始坏帧也不应被宿主诊断回显。

真实 fault command 位于 `runtime/testdata/faultplugin`，同一源码分别构建 native/WASI；它执行加法及实例内计数，并能真实 panic、CPU 死循环、阻塞读、发送畸形帧、stderr 洪泛及写出 stderr 后立即退出。integration 测试构建真实 worker/插件，检查两个实例状态和故障隔离、取消后 reap、环境不继承、WASI 路径不可读和 native 私有 cwd：

```sh
go test -tags=integration -race ./runtime/... -timeout 5m
```

历史验收包含 macOS arm64/amd64、Linux arm64/amd64 和 Windows amd64 执行，具体命令、源码修订及输出见 [OpenSpec 集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)。其中 macOS amd64 使用 Rosetta，Linux amd64 使用容器仿真，Windows amd64 使用 Win11 ARM64 VM 的 x64 仿真，不是物理 amd64 硬件认证。每次改动只继承对应修订的证据；旧结果、交叉编译或 skip 不能算作当前目标通过。
