---
title: 性能测量
description: 用仓库的 Go benchmark 在目标平台记录可复现的会话开销。
---

# 性能测量

仓库提供会话层的 benchmark，在目标平台执行：

```sh
go test -run '^$' -bench . -benchmem ./wire
```

`BenchmarkEndpointNetPipe` 通过内存管道测量一次往返的耗时与分配，分别用 32 B 和 1 KiB 载荷。结果反映当前机器与当前检出：记录 Go 版本、平台、commit 或工作区差异、命令和原始输出，不要引用别的机器上的数字。

它不包含进程创建、包快照和协议协商；真实 native 初始化的开销要围绕实际应用场景测量，独立进程的内存也不在 Host 的 Go 分配统计里。跨进程中间件每一跳需要两次往返，估算链的耗时时按这个成本计。
