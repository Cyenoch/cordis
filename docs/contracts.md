# 数据契约与 Go 代码生成

Cordis 跨进程只传 JSON 数据，不反射任意 Go interface。契约源文件使用 `.schema.json`；`schema/testdata/echo.schema.json` 是包含两个真实方法、可选值、nullable、字节和 int64 的完整声明。生成器只依赖标准库和叶子包 `contract`、`schema`，不依赖宿主实现或 SDK。

## 声明格式

```json
{
  "schema": "cordis-go.contract/v1",
  "services": [{
    "name": "example.echo",
    "major": 1,
    "minor": 0,
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

`name` 转成导出的 Go 标识符，`.`、`-`、`_`、`/`、`:` 分隔单词；例如 `example.echo` 生成 `ExampleEchoClient`。major 必须为正整数，minor 默认为 0。方法和字段转换后的名称发生冲突会报错，而不是重命名或覆盖。声明、方法和数据 schema 的未知字段全部拒绝；不要在声明里写 Go 类型名、函数、闭包、指针、反射规则或 `$ref`。

## 受限 Schema

这不是完整 JSON Schema 实现。省略的功能不作静默兼容：`$schema`、`$id`、`$ref`、`default`、`allOf`、`anyOf`、条件 schema、自定义扩展和未知 format 均拒绝。支持如下字段：

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

`Generate(io.Writer, string, schema.Definition) error` 也允许 SDK/CLI 直接构造声明。生成前检查整个声明与 Go 名称冲突；无效输入不会写出半份文件。写入失败、短写均向调用方返回。生成器输出可直接编译的 Go 源码，不启动工具链或外部进程，调用方可自行安排格式化。

生成的每个方法有独立导出 request/response 类型，例如 `ExampleEchoEchoRequest`，字段有原始 JSON 名的 tag，并生成严格的 `MarshalJSON`/`UnmarshalJSON`。生成器为每种类型缓存已编译 schema；编码、解码两侧都校验，不依赖宿主再次发现坏值。失败的解码不改写接收者。

- 可选字段为 `schema.Optional[T]`，通过 `schema.Some(value)` 设置出现，`Present:false` 完全省略；因此 `false`、0、空串仍能是明确存在的值。
- nullable 类型包装 `schema.Nullable[T]`，`Valid:false` 输出 null，`schema.NonNull(value)` 输出非 null；nullable 对象/数组同样适用，不暴露 Go 指针 ABI。
- number 使用 `json.Number`（可用 `json.Number("0.1")` 构造）；integer 仍是安全范围整数。
- int64/uint64 使用带字符串 JSON codec 的数值类型；字节使用 `[]byte`，数组使用 typed slice。
- union 是拥有多个 `schema.Optional[VariantType]` 字段的具体结构。必须只设置一个分支，并给该分支的 const 字段设置正确 tag；`json.Unmarshal` 自动按 tag 设置对应分支。零个或多个分支都拒绝。
- 非 nullable slice/字节的 nil 值不会自动变成空数组/空字节：它们编码为 null，因而被 schema 拒绝。需要空值时应显式构造非 nil 空 slice。

## 类型化 client/server

```go
client := echo.NewExampleEchoClient(scope)
result, err := client.Echo(ctx, request)

// implementation 满足生成的 ExampleEchoServer interface。
err = echo.RegisterExampleEchoServer(scope, implementation)
```

scope 只需满足生成的两个窄接口：

```go
type Caller interface {
    Call(context.Context, contract.Requirement, json.RawMessage) (json.RawMessage, error)
}
type Registrar interface {
    Provide(contract.Service, contract.Handler) error
}
```

SDK 和宿主 Scope 都可以实现这两个接口。一个 service 注册一个 handler；多方法通过严格的 `{"method":"echo","input":...}` envelope 路由，响应为该方法的原始 response JSON。未知方法、未知 envelope 字段、缺少 input 和无效 request 均拒绝；业务错误原样返回，不自动重试。client 请求声明的精确 minor 版本，不自作兼容性推断。

类型化事件使用公共 `contract.EventKey[T]`、`contract.Subscribe` 和 `contract.Publish`，middleware 使用 `contract.Chain`；参见[组合与事件](composition.md)。这些入口不要求作者导入 core，也不允许跨会话传递任意 Go 对象。

`schema/testdata/consumer_test.go.txt` 是独立消费者的真实实现与行为测试。`go test -tags=integration ./schema` 会生成文件到临时独立 module，使用 replace 指向当前框架，再编译并运行 typed client/server、nullable、可选字段、int64、base64、union 和严格 envelope 场景；普通 `go test ./schema` 运行 schema 与生成输入拒绝测试。已执行结果及对应源码修订以 [OpenSpec change 的集成证据](../openspec/changes/build-go-plugin-framework/tasks.md) 为准，不把本文的复现命令当作新的运行记录。

## 互操作向量

`schema/{package,session,integrity,signature}.schema.json` 是独立 Draft 2020-12 envelope schema；受限契约生成器使用上文自己的数据方言。外部环境提供 Python `jsonschema` 后执行 `python3 conformance/validate-schemas.py`，验证四份 metaschema、58 个结构向量和固定签名字节/SHA-256。该脚本明确不执行 codeVectors 或 Ed25519；后者由 `go test ./packagehost -run '^TestIndependentSigningInteroperabilityVector$'` 校验固定 UTF-8 输入、公钥、签名、信任及篡改拒绝。协议方向、状态、比较式语义和 nullable/64 位 codec 由真实 Go 回归验证，不能由 envelope schema 通过推断。

## 手写 typed 数据

`cordis.Provide/Call`、`sdk.Provide/Call` 和 typed event 共用 `contract.Encode/DecodeData`。非 nullable 标量/struct 拒绝 null，固定数组长度必须精确；匿名嵌入字段遵守 JSON 字段优先级，重复/未知字段仍拒绝。slice/map 的 null 在 typed 数据中也拒绝；显式空值应构造非 nil 容器，显式 null 使用 Nullable。协议 envelope 继续使用 Decode，允许其已声明的可空集合。`schema.Nullable[T]` 的内部值也严格解码，不会吞掉未知字段。

手写 int/uint 在 64 位宿主也不得超出安全整数范围；int64/uint64 应使用 schema 字符串 codec，而非裸 JSON number。
