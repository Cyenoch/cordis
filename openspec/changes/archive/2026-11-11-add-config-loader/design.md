# Design

## Context

动机见 proposal.md。前置：前三个 change：内核、建立在内核上的 `hosting` 和可跨进程的键。

依赖的设施：

- `config` 包把 TOML、YAML、JSON 严格解析为 JSON 数据，并为每个 JSON Pointer 记录行列；`Document.Positions` 与 `Document.Locate` 已经导出，能把路径映射到位置并回退到最近的祖先。它不写文件，对象以排序后的键输出，注释和顺序都不保留。
- YAML 解析已经经过 `yaml.Node`；该库的节点带注释字段并可重新编码。TOML 库没有节点打印能力。
- `contract.Decode` 是严格的 JSON 解码（未知字段、重复键、空值规则），错误携带字段路径（决定 5）。
- `schema` 不支持 `default` 关键字，编译后的 schema 不可遍历。

官方行为的来源是 `third_party/cordis/packages/{loader,include,group}` 的源码与测试，提交 `35fdc96`。

## Goals / Non-Goals

**Goals:**

- 用一份配置文件描述进程内插件和 native 包组成的实例树。
- 运行期编辑安全：先校验、后生效、再写回；写回不破坏使用者手写的文件。
- 错误可定位到文件的行列。
- 加载器本身是一个普通的内核插件，不需要内核的特殊支持。

**Non-Goals:**

- 表达式求值、环境变量替换。
- 就地切换隔离、设置或所属分组而不重启。
- JSON、TOML 的写回。
- 管理 Host 的持久 entry，或新增 CLI 命令。

## Decisions

### 1. 包与依赖方向

| 包 | 职责 | 依赖 |
|---|---|---|
| `loader` | 注册表、条目树、解码、编辑、写回、重新读取、嵌套文件 | `cordis`、`config`、YAML 库 |
| `hosting` | 提供 `(*Host).Resolver() loader.Resolver` | `loader` |

`loader` 不依赖 `hosting` 或 `schema`，也不直接使用 `contract` 的行为类型；`config` 本身导入 `contract` 的数据与解码函数，因此它们被间接带入。native 包通过解析器接口接入，方向是 `hosting` 依赖 `loader`。

### 2. 注册表：显式值，按名称登记

```go
reg := loader.NewRegistry()
err := loader.Register(reg, "sqlite", SQLite)
err = loader.Register(reg, "http", HTTP, loader.Default(func() HTTPConfig { return HTTPConfig{Port: 8080} }))
err = loader.RegisterSetting(reg, HTTPTimeout) // 应用或服务包定义的设置键
```

- `Register` 是泛型函数，把 `cordis.Plugin[C]` 擦除为“解码原始配置并加载”的内部形式。配置类型由插件值推断。
- 默认值的做法是“解码到一份新的默认值上”：`encoding/json` 对未出现的字段保留原值。这是 Go 里常见的默认值写法，不需要 schema 的 `default`。`Default` 接受返回默认值的函数而不是一个值：默认值里的映射、切片和指针若被浅拷贝，会在条目之间共享并被解码改写（已用草稿程序验证）。
- `RegisterSetting` 以设置键的 `Service()` 给出的服务名登记，供条目的 `settings` 解码。
- 不提供全局注册表和 `init` 登记。应用在 `main` 里构造注册表并传入。

解析器接口供注册表之外的来源使用：

```go
type Resolver interface {
    Resolve(name string) (Loadable, bool)
}
```

注册表本身实现它。`host.Resolver()` 解析 `package:<包标识>` 或 `package:<包标识>#<定义>`，把已选中的包定义作为不持久化的 entry 加载在给定作用域下，沿用动态子实例的归属与枚举。包的在用集合除持久 entry 外，把经解析器运行的包以单独的来源计入，使卸载与替换对它们同样被拒绝。

备选与否决理由：

- 全局注册表加 `init` 登记（类似 `database/sql` 驱动）：隐式的全局状态，测试之间互相影响。
- 按配置类型的反射或结构体标签解码：严格解码已经满足，不需要第二套。

### 3. 文件格式

```yaml
plugins:
  - id: db
    plugin: sqlite
    config:
      dsn: app.db

  - plugin: http
    requires: [db]
    settings:
      http: { timeout: 5s }

  - id: tenant-a
    isolate: { db: true }
    plugins:
      - plugin: sqlite
        config: { dsn: a.db }

  - id: extra
    include: extra.yml
    initial:
      plugins: []
    patches:
      - id: cache
        config: { size: 64 }
```

| 键 | 含义 |
|---|---|
| `id` | 条目标识，同一文件内唯一，不含 `:`；可省略 |
| `plugin` | 插件名；与 `plugins`、`include` 三者必居其一 |
| `config` | 原始配置，省略等同空对象 |
| `disabled` | 布尔值 |
| `requires` | 条目级依赖的服务名列表 |
| `settings` | 服务名到设置值的映射 |
| `isolate` | 服务名到 `true`（私有空间）或字符串标签（共享空间）的映射 |
| `plugins` | 子条目列表；没有 `plugin` 时该条目是分组 |
| `include`、`initial`、`patches` | 嵌套文件、其不存在时的初始内容、对它的补丁 |

顶层是带 `plugins` 键的对象。官方的顶层是数组，分组写成 `group: true` 加 `config` 数组；这里用 `plugins` 表达嵌套，避免 `config` 一个键两种含义。

格式按扩展名选择，复用 `config` 的规则。

### 4. 条目到内核的映射

| 条目 | 内核 |
|---|---|
| 分组 | 加载器内部的分组插件的一个实例；子条目加载在它的作用域下 |
| `isolate` | 加载前 `Scope.Isolate(cordis.Name(n))` 或 `IsolateIn(标签对应的空间, …)` |
| `settings` | 加载前用登记的设置键 `With` 派生作用域 |
| `requires` | 加载器内部的包装插件以 `cordis.Name(n)` 声明这些依赖，在其启动函数里加载目标插件；等待原因取自包装实例的状态 |
| 插件 | 注册表或解析器返回的可加载项，在最终作用域下加载 |
| `disabled` | 不加载；分组禁用时不加载其后代 |

这些只用到内核的公开能力。

与官方的差异：官方在 `intercept`、`isolate` 变化或条目移到另一个组时就地替换上下文的原型链，不重启插件。内核的作用域在加载时固定，因此这里重新加载该条目。同组内调整顺序不影响运行中的实例。

### 5. 解码与错误定位

1. 用 `config.Parse` 得到 JSON 数据和位置表。
2. 校验条目结构（未知键、键的类型、三选一、标识格式与唯一性）。
3. 对每个插件条目，把 `config` 子树严格解码到默认配置的副本上。
4. 内核在加载时执行插件的 `Validate`。

第 2、3 步的错误带 JSON Pointer，经 `Document.Locate` 转为文件、行、列。第 4 步的校验错误定位到该条目的 `config` 键。严格解码的错误携带字段路径：`contract` 的解码记录出错字段的路径，`config` 与 `loader` 据此定位，同名字段各自定位到自己的行列。

### 6. 运行期编辑与写回

编辑操作的顺序：校验 → 在条目树上生效 → 写回文件 → 返回。操作不等待实例稳定，需要时调用 `Wait`；它的 ctx 约束的是写文件的时间。

| 变化 | 生效方式 |
|---|---|
| `config` | 实例的配置更新，插件可就地应用 |
| `disabled` | 停止或加载 |
| `plugin`、`requires`、`settings`、`isolate`、所属分组 | 重新加载该条目及其子树 |
| 同组内的顺序 | 不重启 |

同一条目的重新加载合并为一个任务，后到的编辑覆盖先到的目标，只执行一次。

写回：

- YAML：在 `yaml.Node` 树上做最小修改后重新编码，未改动的节点保留注释、顺序和样式。替换一个键时逐层比对新旧值，只换掉变化的部分，所以被编辑的 `config` 内部未变的键也保留注释和引号。新建条目的键顺序固定为 `id`、`plugin`、其余按字母序、`config` 在后。
- 不保存编辑日志。每个条目记着文件里的版本（上次读取或写入时的字段、所在分组和位置），与内存中的字段比较就得到“文件还没有的编辑”。写回时在一份重新解析的 YAML 上按内存的树同步：已有条目只改值不同的键，新条目追加，列表顺序取内存的顺序。失败的写入不会留下改了一半的状态，重试只是再同步一次。
- 写入前把结果解析回来，与内存的树逐项比较，不一致则不写并返回错误。字符串只有在各种 YAML 解析器都认作字符串时才不加引号。
- 写入方式：先核对目标文件自上次读取后是否被改动。被改动时先执行决定 7 的重新读取与三方合并，再基于合并后的内容写入；这与官方“先合并再写”的做法一致。然后写临时文件并重命名替换。
- 写入失败：变更已在内存生效，记录为未写出，下次写入或加载器停止时重试；失败原因经日志和返回值报告。
- JSON、TOML：编辑操作在校验阶段返回只读错误，不生效。

官方在每次写回时整份重新序列化，注释丢失。这里保留注释是本项目的决定。

### 7. 重新读取

`Reload` 读取文件字节，与上次相同则不做任何事。否则解析并校验，失败时保留当前的树并返回带位置的错误。

成功时与当前条目树做差异：

- 按标识匹配；匿名条目按“同一父级下与上次读取的内容相同”匹配。与上次读取的内容比较而不是与内存比较，否则尚未写出的运行期编辑会让匹配失败。
- 新增、移除、变化的条目按决定 6 的规则生效；未变化的条目不动。
- 有未写出的运行期变更时做三方比较（上次读取的内容、文件、内存）：只有一方改动的键取改动方；双方都改动的键取文件，并把被丢弃的变更写入返回值和日志。
- 结构上的变更同样以文件为准：文件定义了未写出的新条目所用的标识时，文件的条目取代它；文件创建或改动了一个待移除子树里的任何内容时，整个移除作废；文件把条目移到别的分组时，未写出的移动作废。文件删掉的条目上未写出的编辑、以及在它下面新建的条目，都作为被丢弃的变更报告。
- 顺序以文件为准；文件没有安排的条目（未写出的新条目、被运行期移动的条目、仍在停止的已移除条目）保持原来的位置。
- 运行期移除的条目在移除写出之前保留其标识，重新读取时才能把它与文件里的同名条目对应起来；写出之后标识立即可以重用。
- 加载失败的嵌套文件在 `Reload` 时重试。

文件监视是可选的轮询：配置 `Watch` 为非零间隔时，加载器按间隔读取各文件并与上次读取的字节比较，不同则调用 `Reload`。默认关闭。配置文件很小，直接比较内容比依赖修改时间可靠。

### 8. 嵌套文件与补丁

`include` 条目在自己的位置上建立一棵子树，拥有独立的文件、位置表和写回。子树条目的全局标识是 `引用条目标识:本地标识`。

- 被引用文件不存在且声明了 `initial` 时，先把初始内容写入磁盘再加载。其他读取或解析错误使引用条目失败，文件不被触碰。
- 补丁按顺序应用在子树内容的副本上：`{insert: […]}` 追加到根，`{id, insert}` 追加到该分组，`{id, 键…}` 替换该条目的整键；补丁里的 `plugin` 只作断言。
- 应用时记录每个条目、每个键的归属：被引用文件、某条补丁。无法应用的补丁记录为未应用并报告。
- 运行期变更按归属分流：归属于补丁的键写回引用方文件的 `patches`，其余写回被引用的文件。
- 补丁追加的条目可以更新，写回该补丁的 `insert`；它们的创建、移除和移动返回 `ErrPatched`，因为这些操作会改变补丁列表里的下标，需要使用者直接改引用方文件。
- 每次重新读取和每次编辑之后，补丁都从“条目不带补丁时的样子”重新计算：不再适用的补丁放开它的键，目标出现后补丁生效，追加的条目随分组消失后会重新追加。被替换的键不参与文件的差异比较，也不写进被引用的文件。
- YAML 承载不了的值（读回来不等于写进去的）在编辑生效前被拒绝。
- 引用条目停止（禁用、重新加载、移除）时先写出子树未写出的变更，然后丢弃子树；再次加载时重新读取文件。
- 文件引用自身（直接或间接）时该条目失败。

### 9. 就绪等待

- `Loader.Wait(ctx)` 在没有条目处于 Starting 或 Quiescing 时返回。Waiting（缺依赖）和 Failed 视为已稳定。实现上先等加载器自己的调度结束，再逐个调用实例的 `Wait`，最后核对期间没有新的活动；刚加载、尚未进入 Starting 的实例（Waiting 且没有缺失项）算未稳定。
- 条目的启动不阻塞调度：加载后立即处理下一个条目。停止和配置更新会等待该实例，保证“先停后启”的顺序。
- `loader.Ready` 是 `Service.Where` 构造的条件依赖，条件为“没有未稳定的条目”。加载器只在该计数归零时调用 `Service.Changed`。
- 内核缓存条件结果，只在 `Changed` 时重新评估。因此它是启动门：等待者在计数归零时启动；之后计数再变为非零不触发重新评估，已启动的实例不会被停止。若在计数变为非零时也通知，以它为依赖的实例自己的启动就会使条件失效并被停止，形成反复启停。
- 以 `loader.Ready` 为依赖的实例处于 Waiting，不计入未稳定，因此不会互相等待。这与官方 `await` 只在任务排空时通知的做法一致。

### 10. 加载器服务

```go
type Loader struct{ /* ... */ }

func (l *Loader) Entries() []Entry
func (l *Loader) Create(ctx context.Context, parent string, options EntryOptions) (string, error)
func (l *Loader) Update(ctx context.Context, id string, patch EntryPatch) error
func (l *Loader) Remove(ctx context.Context, id string) error
func (l *Loader) Move(ctx context.Context, id, parent string, index int) error
func (l *Loader) Reload(ctx context.Context) error
func (l *Loader) Wait(ctx context.Context) error

func EntryOf(s *cordis.Scope) (id string, ok bool)

var ErrNotFound, ErrReadOnly, ErrPatched, ErrConflict error

var Changed cordis.Event[Change] // 条目标识与变化的种类
```

`Entry` 快照包含标识、插件名、引用的文件、禁用标记、状态、错误、等待原因、文件位置和未应用的补丁。状态取自实例的 `Status()`，外加“未解析”和“已禁用”。

`Changed` 在每次编辑或重新读取改变条目树之后发布，供管理界面刷新，对应官方的 `loader/config-update`。官方的 `loader/entry-init`、`loader/patch-context`、`loader/partial-dispose` 是为就地切换上下文服务的扩展点，这里没有对应机制，不提供。

官方在插件停止自己时把条目写成禁用。内核没有“实例停止自己”的操作，插件需要时用 `EntryOf` 取得标识后显式调用 `Update`；禁用自己会取消自己的 ctx，写回要用不随之取消的 ctx。

加载器停止时，条目作为它的子实例由内核先停止，未写出的变更在加载器自己的清理里写出。两者的先后对文件内容没有影响。

### 11. 测试

- 以官方 `loader/tests` 与 `include/tests` 为基线，逐个用例归类为“移植 / 有意差异 / 不适用”，记录在 `verification.md`。表达式求值相关的用例归为不适用。
- 文件操作使用临时目录；写入失败用只读目录或注入的写入函数模拟。
- 保留注释的写回用带注释的输入和期望输出的文件对照。

## Risks / Trade-offs

- YAML 节点重新编码会改变未触碰部分的排版：草稿程序验证了注释、顺序和引号风格保留，但空行、流式写法里的空格和注释的对齐不保留 → 规格只承诺键、值、顺序和注释；用文件对照测试固定实际输出，文档写明。
- 改隔离或设置会重启条目，比官方代价高 → 在文档和对照表中写明。
- 两份意图来源（加载器文件与 catalog）可能让使用者困惑 → 文档明确各自的范围；native 条目在宿主枚举中标为动态来源。
- 匿名条目按内容匹配，在内容被修改时会被当作“移除旧的、新增新的”而重启 → 文档建议为需要稳定身份的条目写 `id`；运行期创建的条目总是带标识。
- 写入失败后的未写出变更在进程异常结束时丢失 → 失败立即报告；这与官方行为相同。
- 补丁归属的分流逻辑复杂 → 单独的需求与测试组，可独立实现和验证。

## Open Questions

- 文件监视的默认间隔。它是使用者的配置项，默认关闭，取值不影响规格。
