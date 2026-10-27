# 性能测量与边界

本目录的测量入口对应 Task 10.3，不设置吞吐、延迟或分配的数字门禁，也不为追求指标改变服务、取消或生命周期契约。以下命令须从独立的 Cordis module 目录执行，不是 VortexGo 根 module。本文提供可执行测量方法；没有把尚未运行的命令、交叉编译或示例数字记为性能证据。

## 进程内分发与内存 transport benchmark

```sh
go test -run '^$' -bench '^BenchmarkServiceDispatch$' -benchmem -benchtime=1000x -count=5 ./internal/core
go test -run '^$' -bench '^BenchmarkEndpointNetPipe$' -benchmem -benchtime=1000x -count=5 ./wire
```

每个 benchmark 都分别测量 32 字节与 1 KiB 的 JSON 字符串，报告 `ns/op`、`B/op`、`allocs/op`，并逐次校验返回的字节。`SetBytes` 的吞吐基数仅为单方向应用 payload，不是请求与响应的帧字节总量。固定 `-benchtime=1000x`、`-count=5` 使调用次数有限、可复现；不是自适应运行到某个性能阈值。

- `BenchmarkServiceDispatch`：激活一个真实 core provider 后，经 `Engine.Call` 查找并调用 echo handler。激活、等待就绪和关闭不计入 `b.Loop` 的计时与分配区间；计时包含服务分发和返回值比对，不包含业务计算或 JSON 编解码。返回值复用输入，这是分发开销基线，不是有业务负载的复制/解析成本。
- `BenchmarkEndpointNetPipe`：建立真实的两个 wire endpoint、完成 initialize 后，在 `net.Pipe` 上进行 NDJSON 请求/响应；包括帧编解码、关联、协程调度、deadline 管理和返回值比对。**这是同一进程内的内存 transport，不是跨进程 IPC**，不经过内核管道、SDK、worker 或服务注册表。`B/op` 与 `allocs/op` 包括两端在同一宿主进程的分配。

不能把这两个 benchmark 的差值称作“操作系统 IPC 成本”。实际跨进程测量见下一节。

## 真实 native / WASM 子进程测量

测量器不链接 counter 插件源码。插件从自己的 `examples/plugins/counter/go.mod` 独立构建，宿主仅通过公开 Runtime、Launch、Manifest 和会话协议交互。下列 POSIX shell 命令同时适用于有相应 Go 工具链的 macOS/Linux；这不是 Windows 实际执行证据。Windows 应使用本机 shell 构建 `.exe` 并传入相应绝对路径。

```sh
out="$(mktemp -d)"
go build -o "$out/cordis-worker" ./cmd/cordis-worker
go build -o "$out/measure" ./conformance/cmd/measure
(
  cd examples/plugins/counter
  go build -o "$out/counter-native" .
  GOOS=wasip1 GOARCH=wasm go build -o "$out/counter.wasm" .
)
"$out/measure" \
  -worker "$out/cordis-worker" \
  -native "$out/counter-native" \
  -wasm "$out/counter.wasm" \
  -iterations 5 -calls 100 -timeout 60s
```

保留标准输出的 JSON 作为本机证据，并同时记录 CPU 型号、内存、系统版本、供电/负载状态及构建参数。JSON 本身记录 Go 版本、GOOS/GOARCH、GOMAXPROCS、三个产物的绝对路径和 SHA-256、次数和 deadline。测量开始前读取并散列产物，不计入区间；这会影响操作系统文件缓存，因此“cold”不表示冷磁盘/冷页缓存。测量器不会自动删除用户传入的构建产物。

每个 runtime 串行运行指定次数，每次均创建新的 activation 和独立的受管进程；所有 native 样本完成后才运行 WASM 样本。每个 activation 有三个独立区间：

| JSON `phase` | 区间与验证 |
| --- | --- |
| `cold_process_start_initialize` | 从 `Runtime.Start` 前至 initialize 返回并完成注册表验证。包含 native 可执行文件复制及启动，或新 worker 启动、WASM 读取/编译/实例化，以及 counter 配置和服务/事件注册。不包含包安装、签名验证、宿主 Engine 发布、构建或最终关闭。`Start` 返回本身不代表 WASM 已编译，因此不能仅计 `Start` 耗时作为就绪延迟。 |
| `first_subprocess_call` | initialize 后第一次真实 stdio IPC `call`，counter 从 41 加至 42，并验证 payload。 |
| `steady_subprocess_calls` | 同一存活会话内的有限次顺序 `add(1)` 调用，逐次验证 43、44……的返回值；总耗时除以调用数，不含第一调用或最终关闭。 |

启用 `-cache-directory` 后，WASM 初始化区间改标为 `cache_first_start_initialize`（本次调用的第一个 worker）和 `cache_reused_start_initialize`（本次调用后续的新 worker）；区间内容仍与上表初始化相同。这些 phase 是执行顺序标签，不是逐次命中/未命中的观测。

每次都验证只注册 `example.counter@1.0`、`counter.changed` 且没有贡献项。每次响应必须是预期 counter 值、`events=0`、空 `host`；协议、启动、调用、验证或关闭错误均使命令非零退出，不输出假成功报告。宿主不授予任何反向 host capability。每次 startup+initialize 以及每个调用都受 `-timeout` 限制；worker 自身编译/启动限制仍按 Runtime 默认值执行，增大该参数不会覆盖 worker 限制。关闭使用 Runtime 自己的受管进程回收路径，不计入性能区间。

### 延迟与分配的含义

`nanoseconds` 是区间墙钟总耗时，`ns_per_op` 是该区间均值。每个 activation 保留原始区间样本，便于比较多次运行；不是调用延迟直方图，不能从中宣称 p95/p99。计时包含调用方 deadline、返回值 JSON 解码和结果验证，因此它测量的是有效端到端调用，不是只测写管道的时间。

`host_allocated_bytes` / `host_allocations` 使用区间前后的 `runtime.MemStats.TotalAlloc` / `Mallocs` 差值；`host_bytes_per_op` / `host_allocations_per_op` 再除以操作数。这些是**测量宿主 Go 进程**的累计分配，包含区间内宿主后台 goroutine 与校验代码，**不包含 native 插件进程、WASM worker 或 guest 堆**，也不是 RSS、峰值内存、存活对象大小或 WASM 编译分配。`ReadMemStats` 本身位于计时区间外，但采样与 GC/调度仍可能扰动负载。无需强制 GC 或禁用 GC；保留正常运行语义。分配值应与同一方法和环境比较，不应与全进程内 benchmark 的两端分配直接等同。

### WASM 持久编译缓存

`wasm.Options.CacheDirectory` 将显式缓存目录传给 worker，后者使用 wazero `NewCompilationCacheWithDir` / `WithCompilationCache`。默认空字符串禁用持久缓存，测量报告 `compiled_wasm_cache` 说明该模式；每个新 worker 重新编译。设置目录后多个新 worker 可复用同一持久缓存，但每个 activation 的 Runtime、WASM 实例和进程仍独立。

延续上面的构建产物，在宿主创建、仅受信任用户可写的**新空目录**进行首次填充及后续新 worker 测量：

```sh
cache="$(mktemp -d)"
chmod 700 "$cache"
"$out/measure" \
  -worker "$out/cordis-worker" \
  -native "$out/counter-native" -wasm "$out/counter.wasm" \
  -cache-directory "$cache" -iterations 3 -calls 100 -timeout 60s
# 保留同一目录、同一产物及引擎版本，再运行一组全新 worker。
"$out/measure" \
  -worker "$out/cordis-worker" \
  -native "$out/counter-native" -wasm "$out/counter.wasm" \
  -cache-directory "$cache" -iterations 3 -calls 100 -timeout 60s
```

只有调用方确认目录起初为空，才有“首次填充空缓存”的前提；第一次传入已有目录，不能把 `cache_first_start_initialize` 称为 miss。第二条命令的 first 仍表示本次测量顺序，而不是缓存被清空。缓存无逐次 hit/miss 探针；引擎版本、平台或产物不适用时仍可能编译，不能仅凭更短时间宣称命中或给出普适加速比。同一 worker 内 steady 调用不重新编译，也不是跨 worker 缓存测量。

目录必须由受信任宿主独占写入，不在插件可写目录中，也不 preopen 给 guest。测量器不会自动清空、修改所有权或删除调用方缓存；缓存不是签名/完整性信任来源。保留空目录来源、构建版本、产物 SHA-256 与完整报告，区分文件页缓存、持久编译缓存和已启动会话的 steady IPC。

## 证据状态与适用范围

进程内 benchmark、真实 native/WASM 测量和集成/race 的命令、产物摘要、样本与平台记录统一见 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md)。报告明确对应测量时的构建；Adapter 的后续修订不自动继承性能数字。不同版本或平台结果不得拼接成同一次基准，不设性能门禁，也不推导 p95/p99。实际执行目标与仿真方式以该证据记录为准，交叉编译不能替代运行。

counter 负载仅包含一个小 JSON 请求、串行有状态加法和响应校验，不代表高并发、大 payload、反向宿主能力调用、事件扇出、真实磁盘或 UI 工作负载。不同机器、不同构建版本、后台负载及操作系统文件缓存都会影响结果；比较时应保留原始样本与环境，不引入缺少场景依据的数字门禁。
