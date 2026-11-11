---
title: 定时辅助
description: 归属实例的延时、周期、等待、节流与防抖。
---

# 定时辅助

`github.com/Cyenoch/cordis/timer` 是一组函数，不是服务，不需要加载插件，也不需要声明依赖。函数接收调用方的作用域，定时器归属该作用域的实例：实例停止时自动取消。

```go
stop := timer.AfterFunc(s, time.Second, func(ctx context.Context) { /* 一次 */ })
stop = timer.Every(s, time.Minute, func(ctx context.Context) { /* 重复 */ })

err := timer.Sleep(ctx, time.Second)            // ctx 先取消时返回它的错误
for now := range timer.Ticks(ctx, time.Second) { /* ctx 取消时结束 */ }

save, stop := timer.Debounce(s, 300*time.Millisecond, func(ctx context.Context, text string) { /* 静默后执行一次 */ })
send, stop := timer.Throttle(s, time.Second, func(ctx context.Context, n int) { /* 窗口内的调用合并为窗口结束时的一次 */ })
```

## 回调的归属

回调作为实例的受管后台工作运行，因此：

- 延时从“已登记且实例已 Active”起算。在 `Start` 里登记的定时器在 `Start` 返回之后才开始计时。
- 实例的 `ctx` 取消后定时器停止。停止过程中，依赖它的实例和它的子实例清理期间，定时器仍然有效。
- 实例等正在执行的回调返回之后才运行清理，回调不会与清理并发。
- 回调里的 panic 使该实例进入 Failed，不会终止进程。
- `stop` 不等待，可以重复调用，也可以在回调里取消自己。
- 一次性回调触发后不留任何登记。

回调可能与实例的其他回调并发执行，需要自行保证并发安全。

## 节流与防抖

- `Throttle`：窗口内第一次调用立即执行；窗口结束时，如果期间有过调用，以最后一次的参数再执行一次并开始下一个窗口。传入 `timer.NoTrailing` 则丢弃窗口内的调用。
- `Debounce`：每次调用重新计时，静默满时长后以最后一次的参数执行一次。
- 窗口和静默期都从回调返回之后起算，所以回调本身的耗时不计入。
- `stop` 之后或实例停止后再调用返回的函数不产生任何执行。

## 说明

- 每个登记占用一个 goroutine。需要大量定时器时，合并成一个周期任务自行调度。
- `Ticks` 在循环体慢于周期时丢弃多余的节拍，不积压。
- `Every` 与 `Ticks` 的周期必须为正，否则 panic，与 `time.NewTicker` 相同。
- 测试用标准库的 `testing/synctest` 控制时间，这个包没有可替换的时钟。

与官方 Cordis 的对应：`ctx.timeout`、`ctx.interval`、`ctx.throttle`、`ctx.debounce`。官方是一个服务插件，靠 Proxy 得到调用方上下文；这里调用方的作用域是显式参数。
