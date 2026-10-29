---
title: 性能测量
description: 使用当前代码的 Go benchmarks 记录可复现结果。
---

# 性能测量

在目标平台执行：

```sh
go test -run '^$' -bench . -benchmem ./internal/core ./wire .
```

结果反映当前机器与当前检出。记录 Go 版本、平台、commit 或工作区差异、命令和原始输出；不要把 builtin 调用的时间当作 native 进程启动时间。

真实 native 初始化涉及进程创建、快照和协议协商，可围绕实际应用场景测量。独立进程内存不包含在 Host 的 Go allocation 统计里。此页不提供未经本轮复现的性能数字。
