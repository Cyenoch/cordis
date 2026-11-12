# framework-contracts Specification

## Purpose
让插件作者只用 Go 声明服务与插件：契约从接口、结构体和启动函数推导，绑定代码使用作者自己的类型。契约仍是规范化的 schema，供清单、指纹和宿主校验使用；推导不替代声明式契约文件能表达而 Go 类型不能表达的形状。

## Requirements

### Requirement: 从 Go 声明推导服务契约

带 `//cordis:service <name> <major>[.<minor>]` 的接口 SHALL 定义一个服务。每个方法 MUST 以 `context.Context` 为第一个参数，至多再有一个请求参数，并返回 `(T, error)`、`error` 或 `iter.Seq2[T, error]`；返回序列的方法是流式方法。方法的线上名字 SHALL 是 Go 方法名首字母小写。不符合的签名、无法表达的类型和未知的 `//cordis:` 指令 MUST 使生成失败，并指出源码位置。

#### Scenario: 请求与响应的 schema 来自结构体

- **WHEN** 方法的请求是带 `json` 标签的结构体，其中一个字段带 `cordis:"minLength=1"`
- **THEN** 推导出的请求 schema 是封闭对象，没有 `omitempty` 的字段必填，该字段带 `minLength`

#### Scenario: 无法表达的类型

- **WHEN** 请求类型含 `any` 字段
- **THEN** 生成失败，错误指出该字段及其位置，不写出任何文件

#### Scenario: 没有请求或结果的方法

- **WHEN** 方法是 `Reset(ctx context.Context) error`
- **THEN** 它的请求与响应都是空对象，生成的客户端方法签名与接口一致

### Requirement: 映射

数据 schema 中 `object` 的 `additionalProperties` SHALL 是 `false` 或一个 schema。是 schema 时对象是映射：任何键都被接受，每个值 MUST 满足该 schema；映射 MUST NOT 同时声明 `properties` 或 `required`。Go 的 `map[string]T` SHALL 推导为可为 null 的映射；键不是字符串的 `map` MUST 使生成失败。

#### Scenario: 映射的值不满足 schema

- **WHEN** 请求的映射字段里有一个值的类型不符
- **THEN** 请求被拒绝，错误指出该键

### Requirement: 从 Go 声明推导插件定义

带 `//cordis:plugin <id>` 的函数 SHALL 定义一个插件，签名 MUST 是 `func(context.Context, *cordis.Scope, C) error`，`C` 的 schema 即配置 schema。同一注释中的 `//cordis:provides <接口>` 和 `//cordis:requires <接口> [<min>-<max>]` SHALL 声明提供和需要的服务，接口可以来自导入的包。省略范围的 `requires` SHALL 只接受该服务声明的次版本。

#### Scenario: 需要导入包里的服务

- **WHEN** 插件 `requires` 一个在导入包中声明的服务接口
- **THEN** 推导出的契约包含该服务，定义的需求带该服务的方法指纹

### Requirement: 生成的代码使用作者的类型

生成的代码 SHALL 为每个服务提供端点键、端点适配、类型化客户端、`Provide<Service>` 和 `Get<Service>`，为每个插件提供 `cordis.Plugin` 值和定义描述；它 MUST NOT 重新声明作者的类型。跨边界的每个值 MUST 在编码后、解码前按契约校验，同进程的调用也不例外。

#### Scenario: 同一个插件在进程内与 native 包中运行

- **WHEN** 把生成的插件值交给内核或交给 `sdk.Define`
- **THEN** 两种方式下服务的调用结果相同，违反约束的请求都被拒绝

#### Scenario: 超出安全整数范围的值

- **WHEN** 响应里的 `int64` 字段取值超过 2^53-1
- **THEN** 调用以校验错误失败，值不会被截断后送达

### Requirement: 推导出的契约随源码落盘

生成 SHALL 同时写出 `cordis_gen.go` 与 `cordis_gen.json`。没有 `contract`、`contracts` 的作者声明 SHALL 从 `cordis_gen.json` 读取契约，缺少该文件时 MUST 失败并提示先生成。构建 MUST 先重新生成再读取。对同一源码重复生成 SHALL 得到逐字节相同的两个文件。命令行打包 MUST 先按包声明的目标分析 Go 源码，两个生成文件与分析结果不一致时失败并提示重新生成；库的打包入口只读生成文件，不运行 Go 分析。

#### Scenario: 源码改动后未重建就打包

- **WHEN** 作者改了服务接口并重新生成，但没有重新构建
- **THEN** 打包失败，指出声明已变化

#### Scenario: 源码改动后未重新生成就打包

- **WHEN** 作者改了服务接口或配置类型，没有重新生成就运行 `cordis pack`
- **THEN** 打包失败，指出生成文件与源码不一致并提示运行 `cordis generate`，不写出归档

#### Scenario: 为其他平台构建的包

- **WHEN** 包只声明另一个操作系统的产物，源码只在那里编译，并已为它构建
- **THEN** 在本机运行 `cordis pack` 按声明的目标分析源码并成功打包
