---
title: 维护文档站
description: 本地运行、生产校验与 GitHub Pages 自动部署的维护方式。
---

# 维护文档站

站点使用 **Astro 7 + Starlight**，输出纯静态 HTML、CSS、少量交互脚本与 Pagefind 搜索索引，不需要服务器、搜索账号或部署 token。

## 本地运行

使用 `package.json` 声明的 Node 版本（至少 22.23.4；CI 使用 24.22.1）和 Bun 1.4.4：

```sh
bun install --frozen-lockfile
bun run docs:dev
```

开发地址为 `http://localhost:4321/cordis/`。Pagefind 在生产构建时生成索引；验证搜索要用生产预览：

```sh
bun run docs:test
bun run docs:build
bun run docs:preview
```

生产预览地址以命令输出为准，通常为 `http://localhost:4321/cordis/`。

## 文件放在哪里

| 路径 | 职责 |
| --- | --- |
| `docs/*.md` | 唯一正文源；GitHub 与站点共用 |
| `site/astro.config.mjs` | 站点地址、导航、中文搜索和代码主题 |
| `site/src/content.config.ts` | 直接读取现有 docs，从一级标题补全页面标题 |
| `site/markdown.mjs` | 页面标题去重、站内 `.md` 链接与 OpenSpec 链接转换 |
| `site/src/styles/custom.css` | 颜色、字体、阅读排版与响应式样式 |
| `site/src/components/Hero.astro` | 首页代码与角色入口 |
| `scripts/check-docs.mjs` | 校验构建后的链接、锚点与子路径资源 |
| `.github/workflows/docs.yml` | PR 校验和 GitHub Pages 发布 |

新增文章使用中文，在 `docs/` 写 Markdown 并添加一级标题；导航配置中加入对应 slug。站内文章仍用相对 `.md` 链接，引用源码用完整 GitHub URL。构建会将 `../openspec/` 证据链接转换到 GitHub，不复制规划文件到站点。

不要把 `site/dist/`、`.astro/`、浏览器缓存或私钥提交到仓库。

自托管字体使用 SIL OFL 1.1，许可随站点发布：[Manrope](https://cyenoch.github.io/cordis/licenses/manrope.txt)、[JetBrains Mono](https://cyenoch.github.io/cordis/licenses/jetbrains-mono.txt)。

## GitHub Pages 首次设置

仓库 **Settings → Pages → Build and deployment → Source** 选择 **GitHub Actions**。配置入口：[Cyenoch/cordis Pages settings](https://github.com/Cyenoch/cordis/settings/pages)。

也可以由有仓库管理权限的维护者执行一次：

```sh
gh api --method POST repos/Cyenoch/cordis/pages -f build_type=workflow
```

已存在 Pages 站点时使用 `PUT` 更新 `build_type`，不重复创建。

## 自动部署规则

- 推送到 `main`：锁文件安装、测试、生产构建与链接校验成功后，上传静态产物并部署。
- Pull request：运行相同构建和校验，不申请 Pages 写权限、不发布。
- 手动执行：在 [Documentation workflow](https://github.com/Cyenoch/cordis/actions/workflows/docs.yml) 选择 `main` 运行；其他分支只构建。
- 发布 job 只使用 GitHub 提供的 Pages / OIDC 权限；不创建 `gh-pages` 分支，不保存 PAT。
- 构建失败不会进入部署；同一 ref 的构建与部署整条流水线串行，不中断已经开始的生产发布，避免旧构建晚完成覆盖新站点。

目标地址是 **[cyenoch.github.io/cordis/](https://cyenoch.github.io/cordis/)**。首次必须将工作流和源码提交、推送到 `main` 并实际运行成功，才能称为已上线；启用 Pages 或本地构建通过不等于部署完成。

## 子路径与迁移

当前仓库使用 `/cordis/` 项目子路径。在 `site/astro.config.mjs` 配置 `site` / `base`，构建期 Markdown 链接与检查器使用相同子路径。若迁移仓库或改为自定义域名，需要一起更新这些位置及工作流文档，再执行完整构建；不要只改首页链接。
