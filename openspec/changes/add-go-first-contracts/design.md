## Context

`schema.Definition` 是规范化的契约：服务、方法的请求与响应 schema、插件定义。清单、指纹、宿主校验都以它为准。现在它只能由作者手写的契约文件得到，生成器再从 schema 反推 Go 类型。

## Goals / Non-Goals

**Goals:**

- 契约的唯一手写来源是 Go 代码；schema 是推导产物。
- 推导结果与 `encoding/json` 对这些类型的实际编码一致。
- 打包、检查、开发循环读取契约时不需要 Go 工具链。

**Non-Goals:**

- 不支持 `any`、带自定义 JSON 编码的类型（`time.Time` 除外）和递归类型：受限 schema 没有对应的形状。
- 不从 Go 推导带标签的联合；需要它的契约继续用声明式契约文件。

## Decisions

### 指令写在声明上

```go
//cordis:service example.echo 1.0
type Echo interface {
	Say(ctx context.Context, request SayRequest) (SayResponse, error)
	Tail(ctx context.Context, request TailRequest) iter.Seq2[Line, error]
	Reset(ctx context.Context) error
}

//cordis:plugin echo
//cordis:provides Echo
//cordis:requires api.Clock 0-2
func start(ctx context.Context, s *cordis.Scope, config Config) error
```

`provides`、`requires` 引用 Go 标识符，由类型检查解析，可以指向导入包里的接口。方法的线上名字是 Go 方法名首字母小写。没有请求或没有结果的一侧是空对象。`requires` 省略范围时只接受声明的次版本。

没有选在运行时用反射推导：接口的客户端必须是生成的代码，清单也要在不运行插件的前提下得到契约。

### 类型到 schema 的映射跟随 encoding/json

| Go | schema |
|---|---|
| `bool`、`string`、浮点 | `boolean`、`string`、`number` |
| 整数 | `integer`，上下界取类型范围与 JSON 安全整数范围的交集 |
| `int64`、`uint64` 带 `json:",string"` | `string`，`format` 为 `int64`、`uint64` |
| `[]T`、`[N]T` | `array`；切片可为 null（nil 切片编码为 null） |
| `[]byte` | base64 `string`，可为 null |
| `*T` | `T`，可为 null |
| `map[string]T` | 以 `T` 的 schema 为 `additionalProperties` 的 `object`，可为 null |
| 结构体 | 封闭 `object`；没有 `omitempty`、`omitzero` 的字段必填；匿名结构体字段展开 |
| `time.Time` | `string` |

约束标签：`cordis:"minLength=1,maxLength=64,minimum=0,maximum=10,minItems=1,maxItems=8,enum=a|b,pattern=^x"`。`pattern` 只能放在最后，其后的内容都属于正则。

超出安全整数范围的 `int64` 值在边界上校验失败，而不是悄悄丢精度。

### schema 语言增加映射

`object` 的 `additionalProperties` 原先只能是 `false`。现在它也可以是一个 schema：对象成为映射，键任意，每个值满足该 schema；映射不能同时声明 `properties`。配置里按名字索引的一组值是最常见的 Go 形状，没有它作者只能改写成键值对的切片。声明式契约文件同样可以使用，生成的类型是 `map[string]T`。

### 生成的代码用泛型编解码器，不重复类型

`schema.Codec[T]` 把一个 Go 类型和它的 schema 绑在一起：编码后校验，校验后解码。生成的端点和客户端只负责按方法名分发，因此不需要为每个类型生成编解码函数，作者的类型原样使用。同进程的快速路径仍然双向校验。

插件生成为值而不是构造函数，因为启动函数已知：

```go
var EchoPlugin = cordis.Plugin[Config]{Name: "echo", Requires: …, Validate: …, Start: start}
```

### 契约落盘为 cordis_gen.json

`cordis generate` 同时写出 `cordis_gen.go` 和 `cordis_gen.json`（规范化的 `schema.Definition`）。`plugin.toml` 没有 `contract`、`contracts` 时，作者声明从 `cordis_gen.json` 读契约。

这样打包、检查和开发计划不必运行 Go 分析，契约的变化也会出现在代码评审的 diff 里。构建先重新生成，再读取；打包沿用构建描述里的指纹校验，源码改了没重建会被拒绝。

没有选让 `LoadAuthor` 直接分析源码：打包在私有快照里运行，快照未必有可解析的模块环境。

### 分析只用标准库

`go list -e -deps -export -json` 给出文件列表和依赖的导出数据，`go/types` 用导出数据检查根包。已有的 `cordis_gen.go` 不参与检查，引用生成符号造成的错误忽略；用到的类型本身不合法才报错。导入包里的服务指令从该包源码的注释读取。

没有引入 `golang.org/x/tools/go/packages`：宿主程序也链接 `packagehost`，不该为此带上它。

## Risks / Trade-offs

- [两种写契约的方式并存] → 一个包只能用一种，由 `plugin.toml` 是否有契约键决定；文档以 Go 为先。
- [生成物过期] → 构建和开发循环总是先生成；打包校验指纹。手动 `go build` 的过期与今天的 `cordis_gen.go` 相同。
- [切片可为 null 让契约比作者想的宽] → 这是 `encoding/json` 的真实行为，写窄了会让合法的 Go 值在边界上失败。
