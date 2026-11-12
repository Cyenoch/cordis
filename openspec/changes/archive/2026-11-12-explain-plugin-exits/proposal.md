## Why

插件进程在调用中退出或往 stdout 写了非协议内容时，调用方只看到 `read frame: EOF` 或 `read JSON token: invalid character …`，看不出是插件以退出码 3 退出，还是插件把日志打到了 stdout。退出码其实已经记在 Outcome 里，只是错误文本没有用上。

## What Changes

- native 会话意外退出时，错误说明 activation 未经 shutdown 就退出，并带上退出码或信号。
- 对端在 stdout 上违反协议时，错误指出 stdout 只承载协议帧、日志应写 stderr。
- 调用因提供方会话结束而失败时，宿主在一个宽限内等会话结束，用会话的最终原因代替传输层的 EOF；超过宽限仍用传输原因。

## Impact

- `runtime/internal/process`、`hosting` 的错误文本；分类（`not_ready`、Outcome）不变。
- `cordis run`、Host API 调用方看到的错误。
