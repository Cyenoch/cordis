# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

用户委托选择现代、稳定的框架，部署为 GitHub Pages 并自动发布。采用 Astro 与 Starlight，版本和实施决策以 `package.json` 与 `openspec/changes/archive/2026-10-29-simplify-native-composition/` 为准。

## Users

中文 Go 插件作者与宿主集成者，需要从可运行示例学习独立插件开发、加载、服务调用和生命周期管理。

## Product Purpose

文档站让初次接触 Cordis 的开发者完成首个插件调用，并按角色找到后续指南。内容来自现有 `docs/`、公开 API 与真实示例，不建立第二份行为规格。

## Capabilities and Constraints

- 纯静态站点、中文本地搜索、代码高亮与复制、移动导航、浅色与深色主题。
- Cordis 的 Go module 为 `github.com/Cyenoch/cordis`，MIT；原生插件不是权限沙箱，资源清理不是业务回滚。
- 项目未发布，公开契约与示例直接同步修改，不保留兼容层；网站不引入运行时后端或领域宿主依赖。

## Brand Commitments

用户明确要求技术新、现代、美观；不沿用初稿的纸色手册外观。其余视觉选择由本次实现提出，不宣称用户已确认具体配色。

## Evidence on Hand

`docs/`、`examples/basic-host/`、`examples/plugins/`、`cmd/cordis/` 与 OpenSpec 验收记录。平台认证和性能数据仅按实际记录引用，不虚构稳定版本或发布成功。
