---
title: Cordis
description: Go 插件组合与 native 进程执行。
template: splash
hero:
  title: 插件独立交付，应用持续生长。
tableOfContents: false
---

# Cordis

Cordis 解决 Go 应用里插件的组合与交付：插件声明服务与依赖，框架在依赖就绪后启动实例，停用时按所有权和依赖顺序释放资源。同一个插件可以在应用进程里运行，也可以作为 native 进程以签名 ZIP 包交付。

```go
var Greeter = cordis.Plugin[Config]{
    Name: "greeter",
    Start: func(ctx context.Context, s *cordis.Scope, c Config) error {
        s.Defer(func(context.Context) error { fmt.Println("bye"); return nil })
        fmt.Println("hello,", c.Name)
        return nil
    },
}
```

Go module 为 `github.com/Cyenoch/cordis`，MIT 许可。

| 目标 | 入口 |
|---|---|
| 运行第一个 native 插件 | [快速开始](getting-started.md) |
| 理解实例、作用域、服务与清理 | [核心概念](concepts.md)、[进程内内核](kernel.md) |
| 用配置文件组装插件，加日志与定时器 | [配置文件加载器](loader.md)、[日志服务](logging.md)、[定时辅助](timers.md) |
| 理解依赖、隔离与更新规则 | [组合与生命周期](composition.md)、[组合实操](composition-tutorial.md)、[资源清理](resources.md) |
| 编写并发布 native 插件 | [编写插件](plugin-author.md)、[数据契约](contracts.md)、[发布插件](plugin-release.md) |
| 在 Go 应用里加载插件 | [接入宿主](host-guide.md)、[API 导航](api.md) |
| 开发循环、包管理和前台运行 | [CLI](cli.md)、[包格式](packages.md)、[多插件联合开发](multi-package-dev.md) |
| 对照官方 Cordis | [与官方 Cordis 的对应](composition.md#与官方-cordis-的对应) |
| 排查与运维 | [排障](troubleshooting.md)、[运维](operations.md)、[恢复](recovery.md) |

native 进程拥有启动用户的权限，签名证明来源与完整性，不是沙箱。资源清理不会撤销已经发生的业务副作用。应用策略通过普通服务实现。
