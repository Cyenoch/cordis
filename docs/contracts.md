---
codeCommit: e6e5f172b7e6dd1099edf8e5f0016432548383a9
codePaths:
  - contract/data.go
  - contract/json.go
  - contract/event.go
  - contract/middleware.go
  - schema
  - conformance/validate-schemas.py
  - packagehost/interoperability_test.go
---

# 数据契约与 Go 代码生成

Cordis 跨进程只传 JSON 数据，不反射任意 Go interface。契约源文件使用 `.schema.json`。从[生成服务实操](generated-service.md)可以跑通生成、注册和调用；`schema/testdata/echo.schema.json` 则提供包含两个方法、可选值、nullable、字节和 int64 的完整声明。生成代码只依赖标准库和 `contract`、`schema`，不依赖宿主实现或 SDK。

## 声明格式

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

`name` 转成导出的 Go 标识符，`.`、`-`、`_`、`/`、`:` 分隔单词；例如 `example.echo` 生成 `ExampleEchoClient`。作者使用 `version:1` 表示 1.0，或 `version:"1.2"` 表示 1.2；拒绝浮点数、补丁位及旧 `major/minor` 字段。方法和字段转换后的名称发生冲突会报错，而不是重命名或覆盖。声明、方法和数据 schema 的未知字段全部拒绝；不要在声明里写 Go 类型名、函数、闭包、指针、反射规则。本地引用只接受 `#/types/Name` 或 `#/$defs/Name`，不得附加同级约束；网络、文件引用、未解析引用和环均拒绝。

## 定义、元数据与指纹

`Definition` 除 `services` 外可包含 `types` 和 `definitions`。例如：

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

生成的 `PanelConfig`、`PanelDefinitionSpec()` 和 `NewPanelDefinition(start)` 共用上述配置与描述。`start` 签名为 `func(context.Context, contract.Scope, PanelConfig) error`；包装在调用前严格解码配置。若 definition 声明消费者 `source`，还生成 `PanelSourceConsumer()`，供 client 和实例登记复用同一对象，而非重新手写范围。

## 受限 Schema

这里使用受限数据方言，不是完整 JSON Schema。`$schema`、`$id`、`default`、`allOf`、`anyOf`、条件 schema、自定义扩展和未知 format 均拒绝，不会静默忽略。根可包含 `$defs`，其中所有定义（包括未使用的定义）均校验；引用在编译前展开。支持如下字段：

| 数据 | 必需字段 | 可选约束 |
| --- | --- | --- |
| 对象 | `"type":"object"` | `properties`、`required`、`additionalProperties:false` |
| 数组 | `"type":"array"`、`items` | `minItems`、`maxItems` |
| 字符串 | `"type":"string"` | `minLength`、`maxLength`、Go RE2 `pattern`、`format`、`contentEncoding` |
| 布尔 | `"type":"boolean"` | 通用约束 |
| 数值 | `"type":"number"` 或 `"type":"integer"` | `minimum`、`maximum` |
| 标记联合 | `oneOf`、`discriminator:{"propertyName":"kind"}` | 通用约束；联合本身不能再声明 `type` |

通用字段为 `nullable`（布尔值）、`enum`（非空数组）、`const`、`title`、`description`。后两项只作作者注释；其余参与校验。`const`/`enum` 中的每个值也必须满足本 schema。schema 自身重复 key、非法范围、无效 pattern、未声明 required、重复 enum 值等在编译时拒绝。

对象始终封闭：即使未写 `additionalProperties`，未知属性仍拒绝。字段缺失与 `null` 不等价；允许 null 必须显式写 `nullable:true`，不能使用 `type:["string","null"]` 或隐式指针。`required` 只控制字段是否必须出现。字符串长度按 Unicode 码点计算；pattern 不会自动加 `^` 或 `$`。

数值必须可表示为有限 float64，溢出和非零下溢拒绝。整数的绝对值不得超过 `9007199254740991`，包括以指数或小数形式书写的数学整数。生成的 number 使用 `json.Number` 保留词法值，避免成功解码的 const/minimum 在重编码时因舍入失败；普通手写 float64 类型仍遵循 float64 语义；如需十进制定点精度，应自行用受约束的字符串表达，而非 number。minimum/maximum 比较使用输入数字的精确有理数，避免校验阶段先舍入。

64 位整数必须是字符串：`{"type":"string","format":"int64"}` 或 `uint64`，使用完整类型范围内的规范十进制，不允许前导 `+`、前导零或 `-0`。字节用 `{"type":"string","contentEncoding":"base64"}`（也接受 `format:"byte"`），要求标准、带 padding 的规范 base64，拒绝换行和非规范尾位。

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

## 调用生成器

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

## 标准编辑器 Schema

```go
standard, err := schema.Export(specs[0].ConfigSchema)
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

## 类型化 client/server

```go
consumer := echo.ExampleEchoConsumer(1, 3)
client, err := echo.NewExampleEchoClient(scope, consumer)
if err != nil { return err }
result, err := client.Echo(ctx, request)

// implementation 满足生成的 ExampleEchoServer interface。
err = echo.RegisterExampleEchoServer(scope, implementation)
```

scope 使用公共 `contract.Caller/Registrar`，生成文件不再重复声明接口：

```go
type Caller interface {
    Call(context.Context, contract.Requirement, json.RawMessage) (json.RawMessage, error)
}
type Registrar interface {
    Provide(contract.Service, contract.Handler) error
}
```

SDK 和宿主 Scope 都可以实现这两个接口。一个 service 注册一个 handler；多方法通过严格的 `{"method":"echo","input":...}` envelope 路由，响应为该方法的原始 response JSON。未知方法、未知 envelope 字段、缺少 input 和无效 request 均拒绝；业务错误原样返回，不自动重试。client 原样使用传入消费者的闭合 minor 范围，不缩窄、不放宽。构造时检查服务名、major、声明 client minor 是否在范围内及本地方法指纹。指纹来自服务名、major 和规范方法 schema，不包含 provider minor，不强制 provider 的完整指纹相等，也不证明跨 minor 业务语义兼容。同名同 major 但不同方法契约的描述会在调用前拒绝。

类型化事件使用公共 `contract.EventKey[T]`、`contract.Subscribe` 和 `contract.Publish`，middleware 使用 `contract.Chain`；参见[组合与事件](composition.md)。这些入口不要求作者导入 core，也不允许跨会话传递任意 Go 对象。

`schema/testdata/consumer_test.go.txt` 是独立消费者的真实实现与行为测试。`go test -tags=integration ./schema` 会生成文件到临时独立 module，使用 replace 指向当前框架，再编译并运行 typed client/server、nullable、可选字段、int64、base64、union 和严格 envelope 场景；普通 `go test ./schema` 运行 schema 与生成输入拒绝测试。已执行结果及对应源码修订以 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md) 为准，不把本文的复现命令当作新的运行记录。

## 互操作向量

`schema/{package,session,integrity,signature}.schema.json` 是独立 Draft 2020-12 envelope schema；受限契约生成器使用上文自己的数据方言。外部环境提供 Python `jsonschema` 后执行 `python3 conformance/validate-schemas.py`，验证四份 metaschema、58 个结构向量和固定签名字节/SHA-256。该脚本明确不执行 codeVectors 或 Ed25519；后者由 `go test ./packagehost -run '^TestIndependentSigningInteroperabilityVector$'` 校验固定 UTF-8 输入、公钥、签名、信任及篡改拒绝。协议方向、状态、比较式语义和 nullable/64 位 codec 由真实 Go 回归验证，不能由 envelope schema 通过推断。

## 手写 typed 数据

`cordis.Provide/Call`、`sdk.Provide/Call` 和 typed event 共用 `contract.Encode/DecodeData`。非 nullable 标量/struct 拒绝 null，固定数组长度必须精确；匿名嵌入字段遵守 JSON 字段优先级，重复/未知字段仍拒绝。slice/map 的 null 在 typed 数据中也拒绝；显式空值应构造非 nil 容器，显式 null 使用 Nullable。协议 envelope 继续使用 Decode，允许其已声明的可空集合。`schema.Nullable[T]` 的内部值也严格解码，不会吞掉未知字段。

手写 int/uint 在 64 位宿主也不得超出安全整数范围；int64/uint64 应使用 schema 字符串 codec，而非裸 JSON number。

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

同一声明图内的类型、方法与 definition 的本地引用展开共享累计 1 MiB 字节预算（包括中间副本）、32 层深度和 10000 节点限制；重复引用也消耗预算，过大的声明在生成或宿主预检前拒绝。这不是 Go 进程的硬内存上限。
