---
title: 运行与维护
description: 上线前的检查、版本替换、关闭与备份、诊断日志和验证。
---

# 运行与维护

## 运行前

确认插件来源与目标平台，配置所需的签名公钥。native 程序拥有启动用户的权限，是否运行由应用决定。

每次激活把插件的可执行文件复制到系统临时目录下再启动。临时目录挂载了 `noexec` 时插件无法启动，把 `TMPDIR` 指到一个允许执行的目录。

## 更新

先 `Install` 新包，再 `Replace` 当前版本。配置与依赖检查失败时保留旧实例；新代码启动失败时保留失败状态，应用显式决定下一次 `Update`、替换或停用。不要自动重放先前的业务调用。

运行中的前台 Host 可用 `-control-file` 开放[本机在线控制](local-control.md)，从另一个终端查询、启停或人工 retry。需要有限自动重试时另加 `-recovery` 策略；没有策略的实例保持人工恢复，见[恢复](recovery.md)。

## 退出与备份

用独立的有限 context 调用 `Close`。超时只结束本次等待，不表示不合作的进程内插件已被终止。`Host.Done()` 才表示排空和目录释放完成；`Close` 即使返回了已有的失败，也可能已经完成，应同时保留错误并检查完成信号。备份持久目录应在 Host 完成关闭后进行，避免复制未完成的写入；业务数据按应用自己的方式备份。

## 诊断

给 `Options.Logger` 设置 `slog.New(slog.NewJSONHandler(writer, nil))` 即可输出结构化诊断。每次激活写一条 `activation started`（含 `startup` 启动耗时）和一条结束记录：`activation stopped`、`activation canceled` 或 `activation failed`，带 `reason` 与已知的 `exitCode`、`signal`、`forced`；`activation failed` 另带 `errorCode`、`error`，清理失败时带 `cleanupCode`；未完成启动就结束的带 `stage: "starting"`。所有记录带 `package`、`entry`、`activation`、`digest`，子 entry 另带 `parent`，父 entry 是 native 时还带 `parentActivation`，可与 `Status` 和运行历史对应；同一个包的多个实例各有独立身份。`instance state` 记录来自内核，名称是内部的。handler 同步执行，应及时返回，不得重入 Host 的管理方法。

包操作的 outcome 分为 `rejected`、`committed`、`committed_error` 和没有变更的 `unchanged`。提交后失败仍须处理返回的错误，不应直接重试。运行历史保留最近 256 条终态，含退出与清理类别；它不是实时进程探测，也不是完整审计日志。

原始 stderr 需显式配置 `Options.Stderr`，进程内插件的 panic 栈需显式开启 `CapturePanic` 并配置 Logger。错误文本、原始输出和栈可能含业务数据，配置及调用内容不会被框架自动写进诊断。native 进程被强制终止时可能没有栈。

## 调用观测

`Options.OnCall` 在每次路由到 native entry 的调用或流结束后被调用一次，用来接指标和追踪：

```go
hosting.Open(hosting.Options{
	OnCall: func(call hosting.CallRecord) {
		calls.WithLabelValues(call.Entry, call.Service.Name, call.Method, string(contract.ErrorCode(call.Err))).Inc()
		latency.WithLabelValues(call.Entry, call.Method).Observe(call.Duration.Seconds())
	},
})
```

记录带 entry、activation、服务、方法、是否为流、流已交付的条目数、耗时和调用方得到的错误，不含请求与响应的内容。`Duration` 从调用方发起算起，包含等待在途名额的时间；流还包含消费者处理各项之间的时间，所以会话饱和或消费者偏慢都会体现在它上面。进程内提供方、插件对 Host 的调用、事件投递和链的各跳不在其中。回调在调用方的 goroutine 上执行，应及时返回。实例状态的变化从内核的 `cordis.InstanceChanged` 事件得到。

## 验证

改动后运行相关的行为测试，再执行 `bash conformance/run.sh`（Windows 用 `conformance/run.ps1`）。只有实际执行过的平台才算验证过，本地编译通过不代表其他平台已运行；步骤见[生命周期验收](lifecycle-acceptance.md)。
