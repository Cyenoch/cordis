# 维护文档站

站点使用 Astro 与 Starlight，直接读取 `docs/*.md`。Node 版本见 `.node-version`，Bun 与依赖版本见 `package.json`、`bun.lock`。正文服务于插件作者和 Host 开发者，本文件保留站点维护说明。

## 开发与验证

```sh
bun install --frozen-lockfile
bun run docs:dev
```

提交前执行：

```sh
bun run docs:test
bun run docs:examples
bun run docs:build
```

生产构建生成静态页面和 Pagefind 搜索索引，并检查内部链接、锚点和未解析变量。需要本地查看时使用 `bun run docs:preview`，地址以命令输出为准。

## 文档与源码同步

项目尚未发布，文档直接对应当前检出。公开 API 变动时同次修改文档与示例，不再为每页维护另一份历史提交映射，也不查询远程发行版。

`site/build-data.mjs` 从当前 `go.mod` 读取 `modulePath`、`goVersion`。正文和代码可使用 `{{docs.goVersion}}` 等变量；标题、链接和 front matter 不使用变量。未知变量和缺少配置使构建失败。Markdown 源文件不回写，直接阅读时可从 `go.mod` 查看实际值。

`docs:examples` 从组合实操、生成服务和快速开始提取真实代码，在临时独立 module 中 replace 到当前检出，再构建、运行并比对输出。快速开始还验证 CLI 的 init、build、pack 和 native 调用。测试也属于通用 conformance 入口。

## 文件分工

| 路径 | 职责 |
|---|---|
| `docs/*.md` | 正文，GitHub 与站点共用 |
| `site/astro.config.mjs` | 导航、中文搜索、代码主题与站点路径 |
| `site/src/content.config.ts` | Markdown 内容加载与页面元数据 |
| `site/markdown.mjs` | 变量渲染、标题去重与内部链接转换 |
| `site/build-data.mjs` | 当前 module 与 Go 版本 |
| `site/src/components/Hero.astro` | 首页展示 |
| `site/src/styles/custom.css` | 字体、颜色、排版与响应式样式 |
| `scripts/check-docs.mjs` | 生产 HTML 的链接与锚点验证 |

## 部署

`.github/workflows/docs.yml` 对 PR 运行验证；推送 main 或手动在 main 执行时，在验证通过后上传并部署 GitHub Pages。部署前核对当前 main HEAD，跳过已被后续提交取代的构建。

Pages Source 需为 GitHub Actions。工作流使用 GitHub Pages / OIDC 权限，不维护发布分支或额外 token。仅本地构建通过不能宣称已部署。

当前路径为 `/cordis/`。改仓库地址或自定义域名时，同时更新 `site/astro.config.mjs`、Markdown 链接转换和检查器，再执行完整构建。
