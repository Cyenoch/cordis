# framework-contracts Specification

## Purpose
让插件作者只用 Go 声明服务与插件：契约从接口、结构体和启动函数推导，绑定代码使用作者自己的类型。契约仍是规范化的 schema，供清单、指纹和宿主校验使用；推导不替代声明式契约文件能表达而 Go 类型不能表达的形状。

## Requirements

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

生成 SHALL 同时写出 `cordis_gen.go` 与 `cordis_gen.json`。生成 MUST 在写出前用解析契约的同一入口重新解析将要写出的 `cordis_gen.json`，无法解析时失败、指出该文件路径并且两个文件都不写。没有 `contract`、`contracts` 的作者声明 SHALL 从 `cordis_gen.json` 读取契约，缺少该文件时 MUST 失败并提示先生成。构建 MUST 先重新生成再读取。对同一源码重复生成 SHALL 得到逐字节相同的两个文件。命令行打包 MUST 先按包声明的目标分析 Go 源码，两个生成文件与分析结果不一致时失败并提示重新生成；库的打包入口只读生成文件，不运行 Go 分析。

#### Scenario: 源码改动后未重建就打包

- **WHEN** 作者改了服务接口并重新生成，但没有重新构建
- **THEN** 打包失败，指出声明已变化

#### Scenario: 源码改动后未重新生成就打包

- **WHEN** 作者改了服务接口或配置类型，没有重新生成就运行 `cordis pack`
- **THEN** 打包失败，指出生成文件与源码不一致并提示运行 `cordis generate`，不写出归档

#### Scenario: 为其他平台构建的包

- **WHEN** 包只声明另一个操作系统的产物，源码只在那里编译，并已为它构建
- **THEN** 在本机运行 `cordis pack` 按声明的目标分析源码并成功打包

#### Scenario: 规范化后超出预算的契约

- **WHEN** 从 Go 推导出的契约在预算内，补全规范形式后超出累计字节预算
- **THEN** `cordis generate` 失败，错误指出 `cordis_gen.json` 的路径和超出的上限，目录中不出现新的生成文件

#### Scenario: 提供另一个包写出的服务

- **WHEN** 一个包声明提供另一个包的服务，那个包的 `cordis_gen.json` 由 `cordis generate` 写出
- **THEN** 该包的 `cordis generate` 读回那份契约并成功

### Requirement: 引用展开有界

一份契约中类型、方法请求与响应、definition 配置的引用展开 SHALL 共用一份 1 MiB 字节预算、32 层深度上限和 10000 节点上限；一份包清单中各 definition 的配置 schema SHALL 同样共用一份；单独编译的一个 schema 自成一份。预算 SHALL 按复制进展开结果的紧凑编码字节计费，转义带来的增长计入：根文档和每次类型引用按去掉 `$defs` 后的本体计一次，每次 `$defs` 引用在复制前按目标值计一次；文档或类型每次展开时校验其全部 `$defs` 条目，每个条目 SHALL 按一次引用计费。`$defs` 的内容 MUST NOT 随所在文档的本体再计一次。计费 MUST NOT 随 JSON 空白或嵌套深度变化。超过任一上限的契约、清单或 schema MUST 被拒绝，错误说明超出的是哪类上限，`schema.FieldError` 的 `Keyword` SHALL 为 `limit`。

#### Scenario: 同一契约换一种缩进

- **WHEN** 同一份契约分别以紧凑形式和缩进形式解析
- **THEN** 两者得到相同的结果：都被接受，或都因同一上限被拒绝

#### Scenario: 深层嵌套中的大叶子

- **WHEN** 一个 schema 在深层嵌套处有一个约 600 KB 的字符串约束，展开结果小于 1 MiB
- **THEN** schema 被接受

#### Scenario: 引用层层放大

- **WHEN** schema 的 `$defs` 每一层都引用前一层十次，共十层
- **THEN** 在展开结果超过上限之前就被拒绝，错误指出字节预算或节点上限

#### Scenario: 多个小文档引用同一个大类型

- **WHEN** 一份契约的类型约 16 KiB，100 个 definition 配置各自只引用这个类型
- **THEN** 契约因累计字节预算被拒绝

#### Scenario: 清单中的多个配置 schema

- **WHEN** 一份包清单有两个 definition，配置 schema 各自展开约 660 KB，单独检查时都在预算内
- **THEN** 清单被拒绝，错误指出第二个 definition 的配置 schema 和字节预算

#### Scenario: 未被引用的 `$defs` 条目

- **WHEN** 一个 schema 的本体只有类型声明，`$defs` 中一个条目的描述由 10 万个 `<` 组成，没有引用它
- **THEN** schema 被接受，作为契约中 definition 的配置时契约也被接受

#### Scenario: 被多次引用的类型的 `$defs`

- **WHEN** 一份契约的类型在 `$defs` 中有一个约 400 KiB、未被引用的条目，definition 配置引用这个类型
- **THEN** 一个配置引用它时契约被接受；两个配置各引用一次时契约因累计字节预算被拒绝

#### Scenario: 转义后超出预算

- **WHEN** 一个 schema 五次引用 `$defs` 中由 10 万个字符组成描述的条目
- **THEN** 字符为 `x` 时被接受；字符为 `<` 时，转义后的展开结果约 3 MB，因字节预算被拒绝

#### Scenario: 没有引用的 schema 超限

- **WHEN** 一个不含 `$ref` 的 schema 的描述由 20 万个 `<` 组成，转义后超过 1 MiB
- **THEN** 检查返回 `schema.FieldError`，`Path` 为 `/schema`，`Keyword` 为 `limit`，消息指出字节上限
