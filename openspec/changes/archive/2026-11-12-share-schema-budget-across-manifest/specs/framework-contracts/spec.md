## MODIFIED Requirements

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
