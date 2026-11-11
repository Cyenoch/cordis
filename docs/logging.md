---
title: 日志服务
description: 按实例命名的 slog 日志器，可按名称和作用域调级，配置就地生效。
---

# 日志服务

`github.com/Cyenoch/cordis/logging` 是一个内核插件。加载它之后，其他插件取得的是标准库的 `*slog.Logger`，名称默认是插件实例的名称。

```go
logs := logging.Plugin.Load(app.Scope(), logging.Config{
    Level:  slog.LevelInfo,
    Levels: map[string]slog.Level{"db": slog.LevelDebug},
})

var HTTP = cordis.Plugin[Config]{
    Name:     "http",
    Requires: []cordis.Requirement{logging.Service},
    Start: func(ctx context.Context, s *cordis.Scope, c Config) error {
        logger, err := logging.Get(s)
        if err != nil {
            return err
        }
        logger.Info("listening", "port", c.Port)
        return nil
    },
}
```

## 配置

| 字段 | 含义 |
|---|---|
| `Level` | 默认级别 |
| `Levels` | 按日志器名称的级别 |
| `Format` | 默认输出的格式：`text`（默认）或 `json` |
| `Buffer` | 最近记录的容量，零表示 1000 |
| `Output` | 默认输出的去向，空表示标准错误；它是配置的一部分，更新时不带它会回到标准错误 |

更新配置不会重启日志插件，也不会重启使用它的插件；已经取得的日志器立即按新配置工作。

默认输出从不写标准输出：native 插件的标准输出是协议通道。

## 级别的三个来源

一条记录是否输出，按下面的顺序取第一个有值的：

1. 取得日志器时所在作用域的 `logging.Level` 设置，内层优先。
2. 配置里该名称的级别。
3. 配置的默认级别。

```go
verbose := logging.Level.With(scope, slog.LevelDebug)
Plugin.Load(verbose, config) // 这个实例及其后代取得的日志器按 debug 输出
```

名称同理：`logging.Name.With(scope, "api")` 覆盖在该作用域加载的实例的日志器名称。

## 输出目标

任何 `slog.Handler` 都可以挂接为额外的输出目标：

```go
provider, err := logging.Service.Get(s)
remove := provider.AddHandler(s, handler)
```

- 目标归属挂接它的实例，实例停止时自动移除。
- 目标只收到已通过日志服务级别过滤的记录，可以用自己的 `Enabled` 再收窄。
- 目标不能再经本服务写日志，否则会无限递归。
- 目标返回的错误以一条记录写到默认输出，不影响其他目标，也不返回给写日志的代码。默认输出自身出错时没有别处可报。

`provider.Recent()` 按时间顺序返回最近通过级别过滤的记录，供管理界面使用。记录包含日志器用 `With`、`WithGroup` 附加的属性，分组名拼进键里；属性值在写入时求值并保存，不要记录需要及时释放的大对象。

## 生命周期记录

日志插件监听实例的状态变化：进入 Failed 时以错误级别记录并带上错误，其余转换以调试级别记录。记录的名称是发生变化的实例的名称，不包含配置。

内核自身的诊断日志（`cordis.Options.Logger`）与本服务相互独立。

日志器在日志插件停止之后仍然可用，继续写到它取得时的那个服务实例；日志插件重启后，使用方会随依赖重启并取得新的日志器。

## native 进程

native 插件在自己的进程里加载日志插件时，它只管本进程；标准错误仍由宿主按现有方式转发。日志不会跨进程汇集。

与官方 Cordis 的对应：`ctx.logger`、exporter、`logger-console`。这里建立在 `log/slog` 上，不提供 printf 风格的格式串、按名称着色和自定义格式化器；官方按 exporter 配置的级别由 `slog.Handler` 自己的 `Enabled` 承担。
