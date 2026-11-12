## MODIFIED Requirements

### Requirement: 从 Go 声明推导服务契约

带 `//cordis:service <name> <major>[.<minor>]` 的接口 SHALL 定义一个服务。每个方法 MUST 以 `context.Context` 为第一个参数，至多再有一个请求参数，并返回 `(T, error)`、`error` 或 `iter.Seq2[T, error]`；返回序列的方法是流式方法。方法的线上名字 SHALL 是 Go 方法名首字母小写。不符合的签名、无法表达的类型、本包中带 `json` 或 `cordis` 标签的未导出字段和未知的 `//cordis:` 指令 MUST 使生成失败，并指出源码位置。

#### Scenario: 请求与响应的 schema 来自结构体

- **WHEN** 方法的请求是带 `json` 标签的结构体，其中一个字段带 `cordis:"minLength=1"`
- **THEN** 推导出的请求 schema 是封闭对象，没有 `omitempty` 的字段必填，该字段带 `minLength`

#### Scenario: 无法表达的类型

- **WHEN** 请求类型含 `any` 字段
- **THEN** 生成失败，错误指出该字段及其位置，不写出任何文件

#### Scenario: 带标签的未导出字段

- **WHEN** 请求结构体里有一个带 `json` 标签的小写字段
- **THEN** 生成失败，错误指出该字段不会被编码，提示导出它或去掉标签

#### Scenario: 没有请求或结果的方法

- **WHEN** 方法是 `Reset(ctx context.Context) error`
- **THEN** 它的请求与响应都是空对象，生成的客户端方法签名与接口一致
