# Third-party notices

本 module 采用 MIT。框架行为参考 cordiverse/cordis，不包含其 TypeScript 运行时代码：

- 来源：https://github.com/cordiverse/cordis
- 固定提交：35fdc969e4b6c48c48ed44717c7a018324e4dd88
- MIT；Copyright (c) 2021-present Shigma
- 当前只读参考：`third_party/cordis/LICENSE`，许可证副本见 `licenses/cordiverse-cordis.txt`

运行依赖：

| Module | 固定版本 | 许可证 |
|---|---|---|
| github.com/tetratelabs/wazero | v1.12.0 | Apache-2.0 |
| golang.org/x/sys | v0.44.0 | BSD-3-Clause |
| golang.org/x/text | v0.37.0 | BSD-3-Clause |

依赖许可证原文随发布产物包含，见 `licenses/`。没有引入 Vortex GPL 源码、MyGo 或 VortexGo 根 module。

仓库附带的 Agent skills 并非运行依赖：Go skills 来自 samber/cc-skills-golang，
documentation-and-adrs 来自 addyosmani/agent-skills，许可证均为 MIT；完整来源声明、
版本与校验值见各 SKILL.md 和 skills-lock.json。OpenSpec 生成 skills 来自固定 @fission-ai/openspec 1.15.0，
MIT；Copyright (c) 2024 OpenSpec Contributors。完整上游许可见
`licenses/skills-openspec.txt`；其他技能许可见 `licenses/skills-*.txt`。
