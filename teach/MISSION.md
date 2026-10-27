# Mission: 用 Cordis 同时做宿主与插件作者

## Why

用户要在自己的产品里内嵌 Cordis 作为插件系统（宿主侧：信任、catalog、生命周期、能力授权），同时也要以作者身份交付插件（服务、事件、宿主能力调用、插件间依赖）。两侧都要能独立交付，而不是只读懂框架代码。

## Success looks like

- 能写一个最小可用宿主：`Open` + 信任策略 + Runtime、`Install/Select/Enable/Wait`、`Broker` 能力、`Update`、`Disable`，并且 `Close` 错误被处理。
- 能写一个可交付插件：`Definition` + `Provide` + 事件 + `InvokeHost` + 资源清理，附 `plugin.json` 与服务版本区间。
- 遇到类型错配或依赖不满足时，能立刻说出问题落在哪一层（生成期 / 注册期 / 编解码期 / 业务期；包依赖 / 服务需求 / 父子所有权）并给出修法。
- 能跑通 `bash conformance/run.sh`，并把 19 条断言映射到具体行为。

## Constraints

- 工具链 Go 1.27.3；材料中文。
- 教学产物要能当文档站点内容：相对链接、自包含、无绝对路径、无 CDN 依赖。
- 不放仓库根目录，统一收在 `teach/`；不使用 `.gitignore` 隐藏。
- 每节课要短，只交付一个可验证的小胜；练习必须带回读反馈。

## Out of scope

- VortexGo / 游戏域业务与 Adapter（由宿主仓库拥有）。
- 框架内部重构与性能调优（WASM、调度分配）——除非作为阅读练习。
- 上游 cordiverse/cordis（JS 版）的实现细节与 API 迁移。
