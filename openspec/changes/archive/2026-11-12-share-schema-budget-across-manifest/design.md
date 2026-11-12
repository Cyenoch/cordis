## Context

源码事实（`1ef6ac7`，v0.4.0）：

- `packagehost.ValidateManifest` 对清单中的每个 definition 调用 `validateConfigSchema` → `schema.Check`，每次都新建 resolver，各有一份 1 MiB 字节预算和 10000 节点。`decodeManifest` 是 `Inspect`、安装读取不受信任归档时的路径，清单元数据上限 1 MiB。
- `schema/references.go` 的 `resolve` 按整个紧凑根文档（含 `$defs`）的 `encodedSize` 计费；`expandRoot` 为校验每个 `$defs` 条目调用 `reference`，`reference` 再按目标值计一次。类型引用在 `reference` 中按类型值连同它的 `$defs` 计费。`$defs` 中的字节因此至少计两次；`<`、`>`、`&` 每个计 6 字节，合计 12 倍。
- 字节预算与深度、节点上限的错误都是 `field("/schema", "$ref", …)`。
- 编译路径：`resolve` 展开引用，`compile` 构造节点，`normalizeNode` 规范化；三者都在每层解码子树并重新编码，每个节点保留自己的原文 `raw`。

实测（Apple M5 Pro，darwin/arm64，Go 1.27.4，每项连续 3 次，`runtime.MemStats` 每 2 ms 采样；v0.4.0 为 `1ef6ac7` 的源码树，"本变更"为本工作树；数量级参考）：

| 输入 | v0.4.0 | 本变更 |
| --- | --- | --- |
| 1 MiB 清单（1,047,646 字节），457 个 2,230 字节的放大配置 schema（审查的 `amp-head.json`），`decodeManifest` | 47.4–53.1 s，累计分配约 87–88 GiB，接受 | 103–108 ms，累计分配约 199–204 MiB，在第二个 definition 处拒绝 |
| 清单中能放下的最坏单个配置：28 层 `items` 链，1,047,424 字节 `x` 叶子，`decodeManifest` | 144–147 ms，分配约 313–315 MiB，堆峰值 +111–117 MiB | 126–133 ms，分配约 314–315 MiB，堆峰值 +111–117 MiB |
| 同上，叶子为 174,640 个未转义的 `<`（清单 175,746 字节），`decodeManifest` | 329–353 ms，分配约 332–341 MiB，堆峰值 +108–115 MiB | 290–303 ms，分配约 322–332 MiB，堆峰值 +108–115 MiB |
| `schema.Check`：31 层链，1,047,767 字节 `x` 叶子 | 162–176 ms，分配约 338–340 MiB，堆峰值 +113–117 MiB | 143–144 ms，分配约 336–340 MiB，堆峰值 +114–117 MiB |
| `schema.Check`：31 层链，174,627 字节 `<` 叶子 | 342–355 ms，分配约 361–375 MiB，堆峰值 +125–127 MiB | 320–324 ms，分配约 351–375 MiB，堆峰值 +124–129 MiB |
| `schema.Check`：`amp-head.json` | 118–126 ms，分配约 191–198 MiB | 97–104 ms，分配约 186–198 MiB |
| `schema.Parse`：VortexGo `pluginapi/cordis_gen.json` | 37–46 ms，分配约 24–25 MiB | 27–30 ms，分配约 24 MiB |
| `schema.Check`：`$defs` 中 10 万个 `<` 的反例 | 0.5 ms，拒绝（字节预算） | 3–4 ms，接受 |

29 层及更深的 `items` 链放不进清单：清单本身的 JSON 嵌套上限先拒绝它。单个 schema 的成本在两个版本间大致相同，差别只在清单是否能让它重复。

剖析（本变更，`schema.Check` 依次检查上表两条 31 层链与 `amp-head.json`，各 10 次，`pprof` 累计值）：累计分配约 60% 在 `normalizeNode`（其中 `canonicalJSON` 约 41%），引用展开（`resolver`）约 21%，`compile` 约 19%。CPU 剖析中 `normalizeNode` 同样占最大一块，其次是引用展开；采样总时间约三分之二是 GC。

## Goals / Non-Goals

**Goals:**

- 检查一份不受信任的清单，工作量有一个与 definition 数量无关的固定上界。
- v0.3.0 接受的 schema 不因 `$defs` 重复计费被拒绝；计费仍不小于展开结果，含转义。
- 超限错误的 `Keyword` 如实说明是上限，不暗示存在 `$ref`。
- 文档与 design 中的成本说法换成实测数字，并注明机器。

**Non-Goals:**

- 提高 1 MiB、32 层或 10000 节点中的任何一项，或改变节点计数方式。
- 新增总工作量上限，或改写 `compile`、`normalizeNode` 每层重新编码的做法。
- 收紧 `hosting` 激活时、`packagehost` 校验实例配置时对单个配置 schema 的编译：它们处理的是已通过清单检查的包。

## Decisions

### 清单的配置 schema 共用一份预算，经导出的 `schema.Budget`

`schema` 导出 `Budget`：持有一个 resolver，`Budget.Check` 与包级 `Check` 做同样的检查，展开计入这份 resolver 的字节预算和节点数。`Compile` 改为 `NewBudget().compile(data)`，单个 schema 的行为不变。`ValidateManifest` 为每份清单建一个 `Budget`，依次检查所有配置 schema，错误仍带 `definition <id> configSchema:` 前缀。

理由：一份清单与一份契约一样来自同一个不受信任的来源，数量由输入决定。共享后，展开总量不超过 1 MiB、10000 节点，与 definition 数量无关，最坏情况等于一个最坏的单个 schema。模块归属：预算属于 `schema`，`packagehost` 只决定范围，依赖方向不变（`packagehost` → `schema`）。

这比 v0.3.0 严格：v0.3.0 也按 definition 各给一份预算。由契约生成的清单，其配置在契约中已与类型和方法共用一份预算；规范化可能使配置变大，所以不作"必然通过"的保证，`cordis pack` 在写出前运行同一个 `ValidateManifest`，超出时在打包时失败。

拒绝的备选：

- **构造一个只含配置的 `schema.Definition` 再 `Check`**：清单 definition ID 的规则与 Go 导出名不同，还会触发与清单无关的规范化与引用检查；它借用了另一种输入的语义。
- **`CheckAll([]json.RawMessage)` 返回出错下标**：调用方要自己把下标映射回 definition，接口比一个有状态的 `Budget` 更别扭。
- **按 definition 数量限制清单**：数量上限不能约束单个 schema 的放大，也会拒绝无害的大清单。

### 计费：本体去掉 `$defs` 后计，引用只对 `$defs` 目标计

`expandRoot` 删去 `$defs` 后用 `json.Marshal` 重新编码本体，按其长度计费，再展开。`json.Marshal` 对 `json.RawMessage` 做紧凑化并转义 `<`、`>`、`&` 和 U+2028/2029，所以计费等于复制进展开结果的本体字节。根文档和每次类型引用都经过 `expandRoot`，各计一次。`reference` 只对 `#/$defs/` 目标按 `encodedSize` 计费；类型引用的本体由 `expandRoot` 计，类型的 `$defs` 在被引用（含校验时的一次访问）时计。

计费仍不小于展开结果：结果中的每个字节要么来自某次计费的本体，要么来自某次计费的 `$defs` 目标，两者都按转义后的长度计。审查用同样改法的覆盖层做过差分：12,000 多个用例中没有拒绝 v0.3.0 接受的输入，展开结果与计费之比最大 0.879；独立审查对 8,000 个随机契约的探测中最大为 1.0，没有超过计费。

拒绝的备选：**保留根文档整体计费，只在 `$defs` 校验访问时免计**：校验访问也会复制并展开条目，免计会让预算小于实际复制量。

### 超限错误的关键字为 `limit`

字节预算、深度和节点上限的错误改用 `Keyword: "limit"`，`Path` 仍为 `/schema`，消息不变（仍含 `byte limit`、`depth or node limit`）。`docs/contracts.md` 在 `Keyword` 的说明中列出它。

理由：预算按契约或清单累计，没有 `$ref` 的 schema 也会超限，`$ref` 会让调用方去找不存在的引用。`limit` 不是 JSON Schema 关键字，所以不会与真实关键字的错误混淆。

拒绝的备选：**`json`**：它表示输入不是可接受的 JSON（如嵌套过深），而这里的输入本身是合法 JSON。

### 不新增总工作量上限

共享预算之后，检查一份清单的最坏情况就是一个最坏的单个 schema：上表中约 0.14–0.36 秒、堆峰值增加约 110–130 MiB、累计分配约 315–375 MiB。这是与清单内容数量无关的固定常数，与归档本身允许的 64 MiB 文件、256 MiB 展开总量处于同一量级。

在本变更之上试用审查的覆盖层（`expand` 每层输出累计计入，2 MiB 上限），同一台机器实测：31 层链能通过的最大叶子降到 65,114 字节（`<` 叶子 10,852 字节），检查约 10–20 ms、分配约 24 MiB；VortexGo 的契约用掉约 270 KB，能通过。但它重新按深度计费，`TestExpansionBudgetCountsEachByteOnce`（600 KiB 叶子位于 15 层深处）、`TestExpansionBudgetIgnoresWhitespace` 和 `TestBudgetIsSharedAcrossSchemas` 都因工作量上限失败。前者正是现有规格「深层嵌套中的大叶子」要求接受的输入，v0.4.0 也正是为了不再按深度计费才修改了预算。另外约 80% 的分配发生在展开之后的 `compile` 与 `normalizeNode`，在展开阶段设上限只能靠拒绝深层输入来降低成本。要在不改变接受范围的前提下降低这个常数，需要让编译与规范化不在每层重新编码子树，这是独立的改动。

## Risks / Trade-offs

- [清单检查比 v0.3.0 严格] 每个配置 schema 单独在预算内、合计超出的手写清单会被拒绝。由契约生成的配置已在契约的共享预算内，实际出现的可能性低；出现时在打包或安装时明确报错，CHANGELOG 写明。
- [单个 schema 的固定成本仍高] 一个最坏的配置 schema 检查约 0.14–0.36 秒、堆峰值约 +130 MiB；`hosting` 在每次激活、更新与重新加载时编译配置 schema，一个已安装的包每次激活都要付出这一成本。目录每次提交与加载时还会对每个已安装的清单重新执行 `ValidateManifest`，并为每个 entry 重新编译配置 schema，一个最坏的已信任包加 N 个 entry 会让每次修改 store 约付出 (1+N) 倍的这一成本（按源码与单个 schema 的耗时推断，未端到端测量）。这些路径都在信任校验之后，不处理不受信任的输入；之后可按摘要与 definition 缓存编译好的配置 schema。降低常数留给改写编译与规范化的独立变更。
- [节点上限也在清单内共享] 一份清单最多 10000 个展开节点，大约对应上万个最小的配置 schema，远超实际需要。
- [错误关键字变化] 依赖 `Keyword == "$ref"` 判断超限的调用方需要改为 `limit`；Go API 在 v0 次版本间允许调整，记入 CHANGELOG。
