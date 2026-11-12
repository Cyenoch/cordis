---
title: 数据契约与代码生成
description: 用 Go 类型声明跨进程服务与配置，推导受限 Schema 并生成适配代码；声明式契约文件用于 Go 类型表达不了的形状。
---

# 数据契约与代码生成

跨进程的服务、配置和事件只传 JSON 数据，契约把这些数据的形状固定下来：生成的客户端、端点和 Host 都按它校验。契约有两种写法，一个包只用一种：

- **Go 类型**：接口、结构体和少量 `//cordis:` 指令就是契约，`cordis generate` 推导 Schema 并生成适配代码。
- **声明式契约文件**：手写 TOML、YAML 或 JSON，用于 Go 类型表达不了的形状（标记联合）和与非 Go 实现共享的契约。

两者共用同一种受限 Schema 和同一套服务入口。从[生成服务实操](generated-service.md)可以跑通生成、注册和调用；生成代码只依赖标准库、根包 `cordis`、`contract` 和 `schema`，不依赖 Host 实现或 SDK。

## 用 Go 类型写契约

```go
// Echo 是服务。
//
//cordis:service example.echo 1.1
type Echo interface {
    Echo(ctx context.Context, request EchoRequest) (EchoResponse, error)
}

type EchoRequest struct {
    Text string `json:"text" cordis:"minLength=1"`
}

type Config struct {
    Prefix string `json:"prefix,omitempty"`
}

//cordis:plugin main
//cordis:provides Echo
func start(ctx context.Context, s *cordis.Scope, config Config) error {
    return ProvideEcho(s, echo{prefix: config.Prefix})
}
```

`plugin.toml` 不写 `contract`、`contracts` 即采用这种写法。

### 服务与插件

`//cordis:service <name> <major>[.<minor>]` 把接口声明为服务；省略 minor 为 0。方法的第一个参数是 `context.Context`，至多再有一个请求参数，形状如下：

| 方法 | 含义 |
| --- | --- |
| `(ctx) error` | 无请求、无结果 |
| `(ctx, request T) error` | 只有请求 |
| `(ctx) (R, error)` | 只有结果 |
| `(ctx, request T) (R, error)` | 请求与结果 |
| `(ctx[, request T]) iter.Seq2[R, error]` | 流式：每次迭代产出一项 `R` |

线上方法名是 Go 方法名首字母小写，`Echo` 即 `echo`。没有请求或结果的一侧是空对象。

`//cordis:plugin <id>` 加在包级函数 `func(context.Context, *cordis.Scope, C) error` 上，`C` 必须是结构体，它的 Schema 就是配置 Schema。同一注释里的指令声明依赖关系：

- `//cordis:provides <Interface>...`：插件提供的服务。
- `//cordis:requires <Interface> [<min>-<max>]`：插件需要的服务和可接受的 minor 闭区间；省略区间时只接受该服务声明的 minor。

接口可以来自导入的包（`//cordis:requires api.Clock 0-2`），该包须先运行过 `cordis generate`，且生成物与它的源码一致。契约按构建目标的平台推导；一个包只有一份契约，带构建约束的文件在各平台上应推导出相同的结果。签名不符、类型无法表达和未知的 `//cordis:` 指令都使生成失败，错误带源码位置。

### 类型映射

映射跟随 `encoding/json`，Schema 描述的就是这些类型实际编码出的 JSON：

| Go | Schema |
| --- | --- |
| `bool`、`string` | `boolean`、`string` |
| `float32`、`float64` | `number` |
| 整数 | `integer`，范围取类型范围与 ±(2^53-1) 的交集 |
| `int64`、`uint64`，带 `json:",string"` | `string`，`format` 为 `int64`、`uint64`，完整范围 |
| `[]T` | `array`，可为 null |
| `[N]T` | `array`，长度恰为 N |
| `[]byte` | base64 `string`，可为 null |
| `*T` | `T`，可为 null |
| `map[string]T` | 键任意、每个值为 `T` 的 `object`，可为 null |
| 结构体 | 封闭 `object`，未知字段拒绝；匿名嵌入的结构体展开为外层字段 |
| `time.Time` | `string` |

- 没有 `omitempty`、`omitzero` 的字段必填；`json:"-"` 与未导出字段不在契约里。
- 切片、映射和指针可为 null：`encoding/json` 把 nil 切片编码为 null，写窄会让合法的 Go 值在边界上失败。
- 字段自己的同名字段遮住嵌入结构体提升上来的那个；两个嵌入结构体提升出同名字段时生成失败。
- JSON 字段名限于字母、数字和 `_`、`-`、`.`、`$`。
- 数值是整数且绝对值超过 2^53-1 的 `float64`（如 `1e16`）同样在边界上校验失败。
- `int64` 与 `uint64` 是限于 JSON 安全整数范围（±(2^53-1)）的 `integer`，因为 JSON 数字常被对端按 float64 解析，更大的值会悄悄丢精度；超出范围的值在边界上校验失败，而不是被截断。需要完整范围时加 `json:",string"`，值以十进制字符串传输。

以下类型无法表达，生成失败并指出源码位置：键不是字符串的 `map`、`any` 与其他接口、`json.Number`、自带 `MarshalJSON` / `MarshalText` 的类型（`time.Time` 除外）、递归类型、嵌入的指针。标记联合同样不能由 Go 类型推导，改用[声明式契约文件](#声明式契约文件)。

### 约束标签

`cordis` 标签给字段加约束，多个约束用逗号分隔：

```go
Name string `json:"name" cordis:"minLength=1,maxLength=64"`
```

| 约束 | 适用 |
| --- | --- |
| `minLength`、`maxLength` | 字符串，按 Unicode 码点计 |
| `minItems`、`maxItems` | 数组、切片 |
| `minimum`、`maximum` | 整数、数值 |
| `enum=a\|b` | 字符串、整数、数值 |
| `pattern=^x` | 字符串，Go RE2；不自动加 `^` 或 `$` |

`enum` 的候选值用竖线分隔。`pattern` 必须写在最后，其后的内容（包括逗号）都属于正则。约束不适用于字段类型时生成失败。

### 边界校验

跨边界的每个值都在编码后、解码前按契约校验，同进程的调用也不例外，所以进程内与 native 的行为一致。违反契约的请求以 `contract.Invalid` 错误到达调用方，错误带原因。整数字段只接受整数字面量：`10` 合法，`10.0` 与 `1e1` 会被 Go 的解码器拒绝。

### 生成

`cordis generate [DIRECTORY]` 分析目录里的 Go 包（缺省为当前目录），在源码旁写出两个文件：

- `cordis_gen.go`：适配代码，内容见[类型化服务](#类型化服务)。
- `cordis_gen.json`：推导出的契约，格式即下文的声明式契约。随源码提交，契约的变化会出现在评审的 diff 里。

对同一份源码重复生成得到逐字节相同的文件。`cordis build` 与 `cordis dev` 先重新生成；`cordis pack` 先核对两个生成文件与 Go 源码一致，再校验声明指纹：改了契约没有重新生成或重新构建，都会被拒绝。`packagehost.Pack` 与检查只读 `cordis_gen.json`，不运行 Go 分析；缺少该文件时报错并提示先生成。生成需要 `go` 命令和能编译的依赖。

## 声明式契约文件

标记联合和与非 Go 实现共享的契约，Go 类型表达不了。这时手写契约文件：`plugin.toml` 用 `contract`（内联）或 `contracts`（文件列表）选用它，`cordis generate -output FILE CONTRACT...` 从文件生成 Go 类型（[CLI](cli.md#从声明式契约生成)）。声明式契约生成的配置类型、`<Def>Plugin(start)` 和服务入口见下，`cordis_gen.json` 也是这种格式。

### 声明格式

```json
{
  "schema": "cordis-go.contract/v1",
  "services": [{
    "name": "example.echo",
    "version": "1.1",
    "methods": [{
      "name": "echo",
      "request": {
        "type": "object",
        "properties": {"text": {"type": "string", "minLength": 1}},
        "required": ["text"]
      },
      "response": {"type": "string"}
    }]
  }]
}
```

`name` 转成导出的 Go 标识符，`.`、`-`、`_`、`/`、`:` 分隔单词；例如 `example.echo` 生成接口 `ExampleEcho` 与 `NewExampleEchoClient`。作者使用 `version:1` 表示 1.0，或 `version:"1.2"` 表示 1.2；拒绝浮点数、补丁位和 `major`/`minor` 字段。方法和字段转换后的名称发生冲突会报错，而不是重命名或覆盖。声明、方法和数据 schema 的未知字段全部拒绝；不要在声明里写 Go 类型名、函数、闭包、指针、反射规则。本地引用只接受 `#/types/Name` 或 `#/$defs/Name`，不得附加同级约束；网络、文件引用、未解析引用和环均拒绝。

### 定义、元数据与指纹

声明除 `services` 外可包含 `types` 和 `definitions`。例如：

```json
{
  "schema":"cordis-go.contract/v1",
  "types":{"PanelSettings":{"type":"object","properties":{"title":{"type":"string","minLength":1}},"required":["title"]}},
  "definitions":[
    {"id":"panel","config":{"$ref":"#/types/PanelSettings"}},
    {"id":"contribution"}
  ]
}
```

无服务或仅配置合法；省略 `config` 得到封闭空对象 schema，并不绕过根约束。`provides` 是同文档服务名列表；`requires` 项为 `{"name":"source","service":"example.echo","minMinor":1,"maxMinor":3}`。`name` 命名消费者描述，`service` 引用本地服务声明（由它确定 major 和方法契约）；两个 minor 键必须显式出现，0 合法，null 和缺省均拒绝，范围必须包含声明 client minor。

```go
specs, err := definition.Specs() // []contract.DefinitionSpec，可直接赋给 Manifest.Definitions
if err != nil { return err }
fingerprint, err := definition.Fingerprint()
if err != nil { return err }
_ = specs
_ = fingerprint
```

`Specs` 按 ID 排序，为每个 definition 返回唯一 `ConfigSchema`、provider 和带方法指纹的 consumer；每次返回独立数据。`Fingerprint` 返回规范声明图的小写十六进制 SHA-256：展开引用，排序类型/服务/方法/定义/无序约束，规范数字写法和默认封闭对象。它不是原文件字节 hash，不替代包完整性或签名，也不证明二进制行为。

每个 definition 生成三样东西：`PanelConfig`（严格编解码的配置类型）、`PanelDefinitionSpec()`（Manifest 用的描述）和 `PanelPlugin(start)`。`PanelPlugin` 返回 `cordis.Plugin[PanelConfig]`，`start` 的签名为 `func(context.Context, *cordis.Scope, PanelConfig) error`，`Requires` 来自声明，配置在 `Validate` 阶段严格校验。进程内用它的 `Load` 加载，native 入口用 `sdk.Define(PanelPlugin(start))`。若 definition 声明了消费者 `source`，还生成 `PanelSourceConsumer()`，描述与客户端复用同一个范围，不必手写。

### 调用生成器

```go
definition, err := schema.Parse(sourceJSON)
if err != nil { return err }
if err := schema.Generate(destinationWriter, "echo", definition); err != nil { return err }
```

`Generate(io.Writer, string, schema.Definition) error` 也允许 SDK/CLI 直接构造声明。生成前检查整个声明与 Go 名称冲突；无效输入不会写出半份文件。写入失败、短写均向调用方返回。生成器使用标准库 `go/format`，在完整校验及 gofmt 成功后才调用 writer，不启动外部进程。writer 的原子替换仍由调用方负责；不能保护调用前已被 shell 重定向截断的文件。

生成的每个方法有导出 request/response 根类型，例如 `ExampleEchoEchoRequest`，字段有原始 JSON 名的 tag，并生成严格的 `MarshalJSON`/`UnmarshalJSON`。生成器为每种类型缓存已编译 schema；编码、解码两侧都校验，不依赖宿主再次发现坏值。失败的解码不改写接收者。

- 可选字段为 `schema.Optional[T]`，通过 `schema.Some(value)` 设置出现，`Present:false` 完全省略；因此 `false`、0、空串仍能是明确存在的值。
- nullable 字段使用 `schema.Nullable[T]`，`Valid:false` 输出 null，`schema.NonNull(value)` 输出非 null；nullable 对象/数组同样适用，不暴露 Go 指针 ABI。
- 普通 number/integer 叶子使用 `json.Number`，例如 `json.Number("2")`；显式 integer 根使用安全范围的命名 int64 codec。
- 普通字符串、布尔、int64/uint64、字节叶子分别使用 `string`、`bool`、`schema.Int64/Uint64`、`[]byte`；数组使用 typed slice。叶子约束由父 codec 完整保留。`types` 中显式具名根仍生成独立严格 codec。
- union 是拥有多个 `schema.Optional[VariantType]` 字段的具体结构。通过 `New<Union>Variant<Tag>(value)` 构造时自动填充 tag；编码必须恰有一个分支，`json.Unmarshal` 自动按 tag 设置对应分支。零个或多个分支都拒绝。
- 非 nullable slice/字节的 nil 值不会自动变成空数组/空字节：它们编码为 null，因而被 schema 拒绝。需要空值时应显式构造非 nil 空 slice。

多个输入用 `schema.GenerateMany(writer, "api", first, second)` 一次检查整个输出包的符号，碰撞在写入前失败；输入顺序不改变输出。`Generate`、`GenerateMany` 和 CLI 分次生成的不同文件均可共处一包；指纹使用 `<Name>ServiceDeclarationFingerprint` / `<Name>DefinitionDeclarationFingerprint`，不生成固定包级同名常量。显式具名类型由作者避免跨文件重复，真实冲突应通过 `GenerateMany` 在写入前检查。

`schema/testdata/consumer_test.go.txt` 是独立消费者的行为测试。`go test -tags=integration ./schema` 把生成的文件与它写入临时独立 module，用 replace 指向当前检出，再编译并运行：类型化往返、union 与严格服务端、客户端拒绝不匹配的端点。普通 `go test ./schema` 运行 schema 与生成输入的拒绝测试。

## 受限 Schema

两种写法共用受限数据方言，不是完整 JSON Schema。`$schema`、`$id`、`default`、`allOf`、`anyOf`、条件 schema、自定义扩展和未知 format 均拒绝，不会静默忽略。根可包含 `$defs`，其中所有定义（包括未使用的定义）均校验；引用在编译前展开。支持如下字段：

| 数据 | 必需字段 | 可选约束 |
| --- | --- | --- |
| 对象 | `"type":"object"` | `properties`、`required`、`additionalProperties:false` |
| 映射 | `"type":"object"` | `additionalProperties` 为值的 schema，不能同时有 `properties`、`required` |
| 数组 | `"type":"array"`、`items` | `minItems`、`maxItems` |
| 字符串 | `"type":"string"` | `minLength`、`maxLength`、Go RE2 `pattern`、`format`、`contentEncoding` |
| 布尔 | `"type":"boolean"` | 通用约束 |
| 数值 | `"type":"number"` 或 `"type":"integer"` | `minimum`、`maximum` |
| 标记联合 | `oneOf`、`discriminator:{"propertyName":"kind"}` | 通用约束；联合本身不能再声明 `type` |

通用字段为 `nullable`（布尔值）、`enum`（非空数组）、`const`、`title`、`description`。后两项只作作者注释；其余参与校验。`const`/`enum` 中的每个值也必须满足本 schema。schema 自身重复 key、非法范围、无效 pattern、未声明 required、重复 enum 值等在编译时拒绝。

对象始终封闭：即使未写 `additionalProperties`，未知属性仍拒绝。`additionalProperties` 是 schema 时对象是映射：键任意，每个值满足该 schema，生成的 Go 类型是 `map[string]T`。字段缺失与 `null` 不等价；允许 null 必须显式写 `nullable:true`，不能使用 `type:["string","null"]` 或隐式指针。`required` 只控制字段是否必须出现。字符串长度按 Unicode 码点计算；pattern 不会自动加 `^` 或 `$`。

数值必须可表示为有限 float64，溢出和非零下溢拒绝。整数的绝对值不得超过 `9007199254740991`，包括以指数或小数形式书写的数学整数。声明式契约生成的 number 使用 `json.Number` 保留词法值，避免成功解码的 const/minimum 在重编码时因舍入失败；Go 类型的 `float64` 遵循 float64 语义；如需十进制定点精度，应自行用受约束的字符串表达，而非 number。minimum/maximum 比较使用输入数字的精确有理数，避免校验阶段先舍入。

完整范围的 64 位整数用字符串：`{"type":"string","format":"int64"}` 或 `uint64`，使用完整类型范围内的规范十进制，不允许前导 `+`、前导零或 `-0`。字节用 `{"type":"string","contentEncoding":"base64"}`（也接受 `format:"byte"`），要求标准、带 padding 的规范 base64，拒绝换行和非规范尾位。

联合的每个分支必须是不可 nullable 的封闭对象，在 required 中包含 discriminator 属性，该属性必须是带非空字符串 `const` 的不可 nullable 字符串；各分支 tag 唯一。例如：

```json
{
  "oneOf": [
    {"type":"object","properties":{"kind":{"type":"string","const":"text"},"text":{"type":"string"}},"required":["kind","text"]},
    {"type":"object","properties":{"kind":{"type":"string","const":"count"},"count":{"type":"integer"}},"required":["kind","count"]}
  ],
  "discriminator":{"propertyName":"kind"}
}
```

先按 tag 选择唯一分支，再验证整份对象，不通过猜测字段或逐分支试错来选择分支。

## 宿主校验

```go
compiled, err := schema.Compile(configSchema)
if err != nil { return err } // 安装/注册前检查 schema 自身
if err := compiled.Validate(configJSON); err != nil { return err }
```

一次性校验可用 `schema.Validate(schemaJSON, valueJSON)`；仅检查声明用 `schema.Check(schemaJSON)`。编译后的 Schema 不可变，可并发复用。值和 schema 都拒绝重复 key、尾随 JSON、无效 UTF-8，限制嵌套深度 32、单数组 10,000 项。未自动插入 default、截断值或把字符串转成数字。

错误通过 `errors.As(err, &fieldError)` 读取 `*schema.FieldError`：`Path` 是 JSON Pointer（根为 `""`），`Keyword` 为 `required`、`minimum`、`nullable`、`discriminator` 等，`Message` 提供原因。schema 自身错误使用 `/schema` 前缀。调用方可据此定位配置字段，不必解析错误字符串。

## 标准编辑器 Schema

```go
standard, err := schema.Export(specs[0].ConfigSchema) // Go 类型：MainDefinitionSpec().ConfigSchema
if err != nil { return err }
// standard 是 JSON Schema 2020-12；写到 panel.editor.schema.json。
_ = standard
```

导出显式闭合对象，将 nullable 转为 null 分支，union 转为带 required/const 的 `oneOf`，不输出私有 `nullable/discriminator`。`enum/const` 仍约束 null。Go RE2 pattern 不直接冒充 ECMAScript 正则，原规则置于 `$comment`；int64/uint64 与 base64 导出保守 pattern，并注释完整范围、规范尾位和换行规则仍由运行时验证。数字的有限 float64、非零下溢、安全整数限制同样注释，不承诺编辑器等价验证。

JSON 文件通过编辑器关联配置，而不向封闭业务对象添加 `$schema`：

```json
{"json.schemas":[{"fileMatch":["panel.json"],"url":"./panel.editor.schema.json"}]}
```

YAML language-server 使用首行 modeline：

```yaml
# yaml-language-server: $schema=./panel.editor.schema.json
title: panel
```

Taplo 使用已有 schema 注释：

```toml
#:schema ./panel.editor.schema.json
title = "panel"
```

`schema/contract.schema.json` 描述作者声明文件，不是业务配置 schema。独立标准验证可在安装了 Python `jsonschema` 的环境运行 `CORDIS_SCHEMA_STANDARD_VALIDATOR=1 go test ./schema -run TestExportIndependentValidator -v`；它检查 Draft 2020-12 metaschema、导出实例向量及作者声明向量，不向 Go module 添加依赖。

## 类型化服务

每个服务 `Echo`（Go 里是接口名，声明式契约由服务名转成标识符，`example.echo` 为 `ExampleEcho`）生成下列入口：

```go
// 键：同一个 key 用于发布和依赖
var EchoEndpoint = contract.EndpointKey("example.echo", 1)
func EchoRequirement(minMinor, maxMinor uint32) cordis.Requirement
func EchoService() contract.Service // 本包生成时的名称与 minor
func EchoConsumer(minMinor, maxMinor uint32) contract.Consumer

// 一步发布与取得
func ProvideEcho(s *cordis.Scope, implementation Echo) error
func GetEcho(s *cordis.Scope) (Echo, error)

// 端点与客户端
func NewEchoEndpoint(implementation Echo) contract.Endpoint
func NewEchoClient(endpoint contract.Endpoint) (Echo, error)
```

插件 `main` 另生成 `MainPlugin`（`cordis.Plugin[Config]` 的值）和 `MainDefinitionSpec()`（Manifest 用的描述）。完整名单与流式方法见[生成服务](generated-service.md)。

提供方在 `Start` 里发布，消费者取得客户端：

```go
// 提供方
err := ProvideEcho(s, implementation)

// 消费方：插件声明了 requires，Host 保证它存在
client, err := GetEcho(s)
if err != nil { return err }
result, err := client.Echo(ctx, request)
```

端点是跨进程能力的统一形状：方法按名称寻址，进出都是校验过的 JSON。提供方在同一进程时，数据同样编码为 JSON 并在两侧校验，所以调用方拿到的是副本，行为与跨进程一致；提供方在另一个进程时，Host 把调用转给那个进程。`NewEchoClient` 检查端点的服务名和 major，不匹配返回 `incompatible`。

返回 `iter.Seq2[R, error]` 的方法是流式方法：提供方返回序列，调用方遍历；提前停止遍历会取消提供方，迭代以最后一个非 nil 错误结束（[流的规则](protocol.md#流)）。

版本范围由 `EchoRequirement(min, max)` 表达，它进入插件的 `Requires`，Host 只把范围内的 provider 绑给消费者；范围外的 provider 使消费者保持 Waiting。契约里的消费者同时记录本地方法契约的指纹（`Consumer.Contract`）；它标识消费者编译时依赖的方法，包 SemVer、服务版本和会话协议版本相互独立。

事件、查询和中间件链用同样的方式处理跨进程数据，键由作者定义在共享的包里：

```go
var (
    Changed = contract.NewEventKey[ChangedPayload]("example.changed")
    Lookup  = contract.NewQueryKey[LookupRequest, LookupResponse]("example.lookup")
    Render  = contract.NewChainKey[string, string]("example.render")
)
```

`Changed.On(ctx, s, fn)`、`Lookup.Handle(ctx, s, fn)`、`Render.Use(ctx, s, middleware)` 登记，`Emit`、`EmitParallel`、`Ask`、`Do` 触发，语义与进程内的 `Event`、`Query`、`Chain` 相同（见[进程内内核](kernel.md)），区别在于登记和触发是返回 error 的调用，进出数据要满足本页的数据规则，插件在另一个进程时它们经由 Host 路由。Host 应用在 `host.Scope()` 上直接使用同一批键。示例见 `examples/plugins/feed` 与 `examples/plugins/counter`。

## 互操作向量

`schema/{package,session,integrity,signature}.schema.json` 是独立 Draft 2020-12 envelope schema；受限契约生成器使用上文自己的数据方言。外部环境提供 Python `jsonschema` 后执行 `python3 conformance/validate-schemas.py`，验证四份 metaschema、105 个结构向量和固定签名字节/SHA-256。该脚本明确不执行 codeVectors 或 Ed25519；后者由 `go test ./packagehost -run '^TestIndependentSigningInteroperabilityVector$'` 校验固定 UTF-8 输入、公钥、签名、信任及篡改拒绝。协议方向、状态、比较式语义和 nullable/64 位 codec 由真实 Go 回归验证，不能由 envelope schema 通过推断。

## 手写类型的数据规则

`contract.Encode`、`contract.DecodeData` 和 `EventKey`、`QueryKey`、`ChainKey`、`Invoke` 使用同一套数据规则。非 nullable 标量/struct 拒绝 null，固定数组长度必须精确；匿名嵌入字段遵守 JSON 字段优先级，重复/未知字段仍拒绝。slice/map 的 null 也拒绝；显式空值应构造非 nil 容器，显式 null 使用 `Nullable`。`schema.Nullable[T]` 的内部值同样严格解码，不会吞掉未知字段。

手写的 int/uint 在 64 位平台也不得超出安全整数范围；int64/uint64 应使用 schema 字符串 codec，而非裸 JSON number。键的类型参数不符合数据规则（指针、interface、函数、channel、递归类型、裸 int64/uint64、非字符串键的 map）时，登记直接返回错误。

不使用生成器时，可用 `contract.Methods(service, map[string]contract.Handler{...})` 和 `contract.Typed` 组装端点，用 `contract.Invoke` / `contract.InvokeStream` 调用；它们走同一套编码规则。

## 配置文件解析

`config` 是 TOML、YAML、JSON 共用的文件边界，只输出 JSON 数据及来源位置，不依赖 schema 或宿主。它不填业务默认值、不合并环境变量、不处理 include，也不回写文件或保留注释。

```go
document, err := config.Load("settings.toml", "")
if err != nil { return err }
if err := document.Decode(&settings); err != nil { return err }
```

`Load(path, format)` 读取文件；`Parse(source, data, format)` 解析已有字节，`source` 原样用于诊断。`config.TOML`、`config.YAML`、`config.JSON` 显式选择格式；空 format 只认 `.toml`、`.yaml`/`.yml`、`.json` 扩展名，不尝试其他解析器。stdin 应由调用方读取，再以显式格式调用，例如 `config.Parse("stdin", data, config.YAML)`；`Load` 不将 `-` 当 stdin。

- 人手维护的对象配置优先 TOML，支持 table、array table、inline table 和 dotted key；需要 null 或非对象根时用 YAML 或 JSON。
- TOML 没有 null：省略字段仍是缺失，空串和 `"null"` 仍是字符串。YAML、JSON 保留显式 null，也接受数组和标量根。
- 三种格式只映射 JSON 数据模型。日期时间必须写为引号字符串，NaN、Infinity 等非有限原生值拒绝。数字不经 float64 中转；TOML 进制整数精确转为十进制，数字分隔符按词法规则移除，`0.10000000000000001` 保持原值。YAML 的数字解析使用节点原文，不使用依赖库转换后的浮点结果；引号字符串和显式 `!!str` 不被重解释。
- 重复键、重复或冲突 TOML 表、非法 UTF-8、尾随 JSON 数据及 YAML 文档均拒绝。YAML 另拒绝 alias、anchor、merge、自定义 tag、非字符串对象键和 JSON 模型外的原生 tag；引号内的 `<<` 是普通字符串键。

输入上限为 1 MiB，根深度为 0，最大深度 32，每个对象或数组最多 10,000 项。`Load` 最多读取上限加一个字节以检测超限，`Parse` 在进入解析器前检查字节数与 UTF-8。JSON 在消费 token 时检查结构限额；YAML 依赖库先分配整份文档 AST，TOML 依赖库先分配当前顶层表达式 AST，再由本包检查深度和集合限额。因此后两种格式的结构限额不是依赖库分配前的内存或递归硬上限。

`Document.JSON` 是保留精确数字 token 的 `json.RawMessage`，生成后通过 `contract.CheckJSON`。`Decode(target)` 使用严格 `contract.Decode`，拒绝未知字段；schema 校验仍由调用方执行，并不因文件解析成功而省略。

`Document.Positions` 以 JSON Pointer 为键，空路径表示根，行列从 1 开始；对象成员通常定位到键，数组项定位到值。`document.Locate("/database/port", cause)` 保留原错误和请求路径，缺少精确位置时使用最近已知祖先。`*config.Error` 暴露 `Source`、`Path`、`Line`、`Column`、`Cause` 并支持 `errors.Is/As`；语法错误也带来源和可获得的行列。`Decode` 尽可能定位未知字段：同名字段出现多处或自定义 codec 不提供路径时，可能只能定位到根。YAML 依赖库的语法错误通常只提供行号，列号为 0。来源可能包含可信本地路径，对外展示前应由宿主做安全投影。

一份契约内的类型、方法与 definition 配置的本地引用展开共用 1 MiB 字节预算、32 层深度和 10000 节点限制；单独编译的一个 schema 自成一份。预算计的是复制进展开结果的紧凑字节：每个文档计一次，每次引用在复制前按目标计一次，因此与空白和嵌套深度无关，重复引用照样计费。过大的声明在生成或宿主预检前拒绝；`cordis generate` 写出 `cordis_gen.json` 前确认 `schema.Parse` 能读回它。这不是 Go 进程的硬内存上限。
