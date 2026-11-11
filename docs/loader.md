---
title: 配置文件加载器
description: 用一份配置文件组装进程内的插件树，在运行期编辑它并写回文件。
---

# 配置文件加载器

`github.com/Cyenoch/cordis/loader` 把一份配置文件加载成[进程内内核](kernel.md)里的实例树，提供运行期的创建、更新、移除和移动，并把这些编辑写回文件。它本身是一个普通的内核插件。

本页对应可运行示例 [`examples/loader`](https://github.com/Cyenoch/cordis/blob/main/examples/loader/main.go)。

## 注册表

配置文件里只有名字。应用在 `main` 里构造注册表，登记名字对应的插件；没有全局注册表：

```go
registry := loader.NewRegistry()
loader.Register(registry, "greeter", GreeterPlugin,
    loader.Default(func() GreeterConfig { return GreeterConfig{Mark: "!"} }))
loader.Register(registry, "shout", shout(out))
loader.RegisterSetting(registry, logging.Level)
```

- 条目的 `config` 以 JSON 严格解码到 `Default` 返回的新值上：条目没写的键保留默认值，未知键和类型不符是错误。`Default` 接收函数，因此条目之间不共享默认值里的映射和切片。
- `RegisterSetting` 让条目的 `settings` 可以设置该键，键名是它所属服务的名称。

## 文件

```yaml
# 应用的插件。
plugins:
  - id: greeter
    plugin: greeter
    config:
      greeting: hello # 运行期会被改掉

  - id: shout
    plugin: shout

  # 这个分组有自己的 greeter，组内的 shout 绑定它。
  - id: tenant
    isolate:
      greeter: true
    plugins:
      - plugin: greeter
        config:
          greeting: hola
      - id: tenant-shout
        plugin: shout
        settings:
          logger: DEBUG
```

| 键 | 含义 |
|---|---|
| `id` | 条目标识，同一文件内唯一，不含 `:`，不以 `~` 开头。省略时得到一个只在本次运行有效的标识 |
| `plugin` | 注册表里的名字；与 `plugins`、`include` 三者必居其一 |
| `config` | 插件的配置，省略等同空对象 |
| `disabled` | 保留在树中但不加载；禁用分组时它的后代都不加载 |
| `requires` | 额外依赖的服务名，齐备后才加载这个条目的插件 |
| `settings` | 服务名到设置值，作用于这个条目及其后代 |
| `isolate` | 服务名到 `true`（私有空间）或标签（同一标签共用一个空间） |
| `plugins` | 子条目；没有 `plugin` 的条目是分组 |
| `include`、`initial`、`patches` | [嵌套文件](#嵌套文件) |

格式按扩展名选择。YAML 可以写回；JSON 和 TOML 只读，对它们的运行期编辑返回 `loader.ErrReadOnly`。

错误带文件位置，例如 `app.yml:6:7 (/plugins/0/config/nope): unknown contract field "nope"`。出错的条目状态为 `unresolved`，其余条目照常加载。

## 运行

```go
app := cordis.New(cordis.Options{})
root := app.Scope()
logging.Plugin.Load(root, logging.Config{Output: io.Discard})
loader.Plugin.Load(root, loader.Config{File: file, Registry: registry})
```

别的插件通过 `loader.Service` 取得 `*loader.Loader`。要等全部条目稳定后再启动，把 `loader.Ready` 写进 `Requires`：条目第一次全部稳定后它即满足，此后一直满足，所以已经启动的实例不会因为之后的编辑而停止，稳定之后才加载的实例也不再等待。因缺少依赖而等待的条目不算“未稳定”。

```go
cordis.Plugin[struct{}]{
    Name:     "admin",
    Requires: []cordis.Requirement{loader.Ready},
    Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
        l, err := loader.Service.Get(s)
        // ...
    },
}
```

## 运行期编辑

```go
config := json.RawMessage(`{"greeting": "goodbye"}`)
err := l.Update(ctx, "greeter", loader.EntryPatch{Config: &config})
err = l.Wait(ctx)

id, err := l.Create(ctx, "tenant", loader.EntryOptions{ID: "late", Plugin: "shout"})
err = l.Remove(ctx, id)
err = l.Move(ctx, "shout", "", 0)
```

编辑先完整校验，再在树上生效，然后写回文件并返回；它不等实例稳定，需要时调用 `Wait`。`ctx` 约束的是写文件的时间。找不到标识时返回 `loader.ErrNotFound`。没有改变任何东西的编辑不写文件，也不发事件。

`Update` 的 `Config` 替换整个配置；要改其中一个键，先从 `l.Entries()` 的快照里取出当前的 `Config`。`Move` 的下标按 `Entries()` 在该分组里列出的条目计数。

| 变化 | 生效方式 |
|---|---|
| `config` | 实例的配置更新；插件登记了 `cordis.OnUpdate` 时可以就地应用，否则重启 |
| `disabled` | 停止或加载 |
| `plugin`、`requires`、`settings`、`isolate`、所属分组 | 重新加载该条目及其子树 |
| 同组内的顺序 | 不重启 |

`loader.EntryOf(s)` 返回作用域所属条目的标识，插件可以用它编辑自己。把自己禁用会取消自己的 `ctx`，写回要用不随它取消的 `ctx`（例如 `context.WithoutCancel`）。每次编辑或重新读取改变了树，`loader.Changed` 事件给出条目标识和变化的种类。

`go run ./examples/loader` 的输出：

```text
shout: hello, shout! (debug=false)
tenant-shout: hola, tenant-shout! (debug=true)
-- update greeter
shout: goodbye, shout! (debug=false)
-- reject an invalid entry
loader: entry needs exactly one of "plugin", "plugins" or "include"
-- create and disable
late: hola, late! (debug=false)
greeter: active
shout: disabled
tenant: group
-- app.yml
# 应用的插件。
plugins:
  - id: greeter
    plugin: greeter
    config:
      greeting: goodbye # 运行期会被改掉
  - id: shout
    plugin: shout
    disabled: true
  # 这个分组有自己的 greeter，组内的 shout 绑定它。
  - id: tenant
    isolate:
      greeter: true
    plugins:
      - plugin: greeter
        config:
          greeting: hola
      - id: tenant-shout
        plugin: shout
        settings:
          logger: DEBUG
      - id: late
        plugin: shout
```

## 写回

- 只修改编辑涉及的 YAML 节点。其余部分的键、值、顺序、注释和引号保留；空行和注释的对齐不保留。
- 写入前把要写的内容解析回来，与内存中的树逐项比较；不一致就不写并返回错误。
- 写入是临时文件加重命名的原子替换。
- 只有运行期编辑会写文件，读取不会。
- 写入失败或 `ctx` 被取消时，编辑已经在内存生效，错误返回给调用方；它在下一次编辑、`Reload` 或加载器停止时再写。进程异常退出会丢失尚未写出的编辑。

## 重新读取

`l.Reload(ctx)` 重新读取文件并按条目标识做差异：没变的条目不动，变了的按上表生效；加载失败的嵌套文件会再试一次。把 `Config.Watch` 设为一个间隔可以定期检查文件并自动重新读取，默认关闭。

- 文件无法解析或校验失败时，运行中的树不变，错误带位置返回，文件不会被内存内容覆盖。
- 写回之前如果文件已被外部修改，先合并再写。
- 有尚未写出的编辑、而文件改了同一个条目的同一个键时，以文件为准，被丢弃的编辑作为 `loader.Dropped` 错误返回。创建、移除和移动同理：文件定义了同一个标识、改动了待移除的子树、或把条目移到了别的分组时，文件胜出；文件删掉的条目上未写出的编辑也会被报告。
- 没有 `id` 的条目按“同一父级下内容与上次读取相同”匹配。改了它的内容等于移除该条目再新增一个，会重启；需要稳定身份的条目请写 `id`。

## 嵌套文件

```yaml
plugins:
  - id: extra
    include: extra.yml      # 相对于本文件
    initial:                # 文件不存在时用它创建
      plugins: []
    patches:
      - id: cache           # 替换 extra.yml 里 cache 条目的整个 config
        config: {size: 64}
      - insert:             # 向 extra.yml 的顶层追加条目
          - {id: probe, plugin: probe}
```

- 子树里的条目用 `extra:cache` 这样的复合标识查询和编辑。
- 只有文件不存在时才用 `initial` 创建它；存在但读不了或解析失败时，该条目失败，文件不被触碰。
- 补丁不修改被包含的文件。带 `id` 的补丁可以替换 `config`、`disabled`、`requires`、`settings`、`isolate`，也可以用 `insert` 向该分组追加条目；补丁里的 `plugin` 只作断言。无法应用的补丁列在条目快照的 `Unapplied` 里，其余补丁照常生效。
- 运行期编辑按键的来源写回：补丁提供的键写回本文件的 `patches`，其余写回被包含的文件。补丁插入的条目可以更新，增删和移动要改本文件的 `patches`，否则返回 `loader.ErrPatched`。
- 禁用或重新加载 `include` 条目会先写出子树里未写出的编辑，再重新读取文件。

## 与 Host 的关系

加载器文件和 Host 的持久 entry 是两份独立的意图：

| | 加载器文件 | Host 的持久 entry |
|---|---|---|
| 描述什么 | 一个进程里的插件树 | 已安装的 native 包要运行哪些实例 |
| 谁修改 | 使用者手写，或运行期编辑写回 | `cordis` CLI 与 Host 的管理 API |
| 运行期编辑是否写回 | 是（YAML） | 不涉及；加载器不读写它 |

## 与官方 Cordis 的差异

- 顶层是带 `plugins` 键的对象，分组用 `plugins` 表达；官方顶层是数组，分组写成 `group: true` 加 `config` 数组。
- 改变 `settings`、`isolate` 或所属分组会重新加载条目；官方就地替换上下文。
- 写回保留注释；官方整份重新序列化。
- 没有表达式求值和环境变量替换。
- 没有“实例停止自己就写成禁用”的约定，插件用 `EntryOf` 加 `Update` 显式表达。
