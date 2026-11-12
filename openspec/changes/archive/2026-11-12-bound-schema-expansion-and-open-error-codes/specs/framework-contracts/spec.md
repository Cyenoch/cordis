## ADDED Requirements

### Requirement: 引用展开有界

一份契约中类型、方法请求与响应、definition 配置的引用展开 SHALL 共用一份 1 MiB 字节预算、32 层深度上限和 10000 节点上限；单独编译的一个 schema 自成一份。预算 SHALL 按复制进展开结果的紧凑编码字节计费：每个根文档计一次，每次引用访问在复制前按目标值计一次。计费 MUST NOT 随 JSON 空白或嵌套深度变化。超过任一上限的契约或 schema MUST 被拒绝，错误说明超出的是哪类上限。

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

## MODIFIED Requirements

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
