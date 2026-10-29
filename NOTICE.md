# Third-party notices

本 module 采用 MIT。框架行为参考 cordiverse/cordis，不包含其 TypeScript 运行时代码：

- 来源：https://github.com/cordiverse/cordis
- 固定提交：35fdc969e4b6c48c48ed44717c7a018324e4dd88
- MIT；Copyright (c) 2021-present Shigma
- 当前只读参考：`third_party/cordis/LICENSE`，许可证副本见 `licenses/cordiverse-cordis.txt`

运行依赖：

| Module | 固定版本 | 许可证 |
|---|---|---|
| golang.org/x/sys | v0.44.0 | BSD-3-Clause |
| golang.org/x/text | v0.37.0 | BSD-3-Clause |
| github.com/pelletier/go-toml/v2 | v2.2.4 | MIT |
| go.yaml.in/yaml/v3 | v3.0.4 | MIT / Apache-2.0（按文件，见原始许可） |

依赖许可证原文随发布产物包含，见 `licenses/`。

仓库附带的 Agent skills 并非运行依赖：Go skills 来自 samber/cc-skills-golang，
documentation-and-adrs 来自 addyosmani/agent-skills，许可证均为 MIT；完整来源声明、
版本与校验值见各 SKILL.md 和 skills-lock.json。OpenSpec 生成 skills 来自固定 @fission-ai/openspec 1.15.0，
MIT；Copyright (c) 2024 OpenSpec Contributors。完整上游许可见
`licenses/skills-openspec.txt`；其他技能许可见 `licenses/skills-*.txt`。

文档站自托管 Manrope Variable 与 JetBrains Mono Variable 字体（均为 SIL OFL 1.1），来自 Fontsource 固定依赖。完整许可随站点发布在 `site/public/licenses/`，不影响 Go module 的运行依赖或 MIT 许可。
