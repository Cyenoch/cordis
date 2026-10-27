# 维护文档站

本文供 Cordis 仓库贡献者维护文档站，不进入开发者站点、导航或搜索。命令均从仓库根目录执行。

站点使用 Astro + Starlight，版本由 package.json 和 bun.lock 固定；输出静态 HTML、CSS、交互脚本与 Pagefind 搜索索引，不需要常驻服务器、搜索账号或自备部署 token。

## 本地运行

Node 版本使用 `.node-version`，兼容范围见 package.json 的 engines.node；Bun 使用 packageManager 指定的版本。版本变量查询还需要 Go（版本见 go.mod）。CI 读取同一配置，不在本文重复维护版本数字：

```sh
bun install --frozen-lockfile
bun run docs:dev
```

开发地址为 `http://localhost:4321/cordis/`。Pagefind 在生产构建时生成索引；验证搜索要用生产预览：

```sh
bun run docs:test
bun run docs:sync
bun run docs:examples
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
| `site/build-data.mjs` | 构建时读取代码基线的版本，不改写文档源文件 |
| `site/src/styles/custom.css` | 颜色、字体、阅读排版与响应式样式 |
| `site/src/components/Hero.astro` | 首页代码与角色入口 |
| `site/src/components/Footer.astro` | 每页代码基线链接 |
| `scripts/check-docs.mjs` | 校验构建后的链接、锚点与子路径资源 |
| `scripts/check-doc-sync.mjs` | 核对代码基线、关联路径与源码差异 |
| `conformance/docs_integration_test.go` | 从教程正文提取完整程序，在独立 module 中执行并核对输出 |
| `.github/workflows/docs.yml` | PR 校验和 GitHub Pages 发布 |

新增文章使用中文，在 `docs/` 写 Markdown、一级标题与代码基线；导航配置中加入对应 slug。站内文章用相对 `.md` 链接，引用源码用完整 GitHub URL。构建会将 `../openspec/` 证据链接转换到 GitHub，不复制规划文件到站点。

不要把 `site/dist/`、`.astro/`、浏览器缓存或私钥提交到仓库。

自托管字体使用 SIL OFL 1.1，许可随站点发布：[Manrope](https://cyenoch.github.io/cordis/licenses/manrope.txt)、[JetBrains Mono](https://cyenoch.github.io/cordis/licenses/jetbrains-mono.txt)。

## 文档与代码同步

每篇 `docs/*.md` 的 front matter 记录两个字段：

```yaml
codeCommit: 0fa22bc6ba8210c8c9ae24d15be5a3855686041a
codePaths:
  - host.go
  - contract
  - sdk
```

`codeCommit` 是这篇内容核对过的代码 commit。核对当前检出时，用 `git rev-parse HEAD` 取得完整 40 位 SHA。它不是文档最后修改时间、发布标签或测试通过证明。页尾显示短 SHA，点击查看该次代码；在 GitHub 直接阅读时可在原始 Markdown 中看到完整记录。

`codePaths` 填仓库相对文件或目录，覆盖本页依赖的实现、工具和示例。目录会包含其中新增、修改及删除的文件；不支持 glob，不填 `../`，也不要为减少警告而删去真实关联。根包的文件逐一列出，不用仓库根目录把所有文档变化都算作 API 变化。

```sh
bun run docs:sync
```

检查逐页比较基线与当前工作树，包含已提交、暂存、未暂存以及未跟踪文件。相关路径没有差异，只表示这些文件内容一致，不代表已执行教程或审查了所有行为。路径有变化会列出受影响页面和文件，CI 以 warning 提醒，不自动更新 SHA，也不因机械差异阻塞发布。缺字段、非法路径、找不到 commit 或 commit 不属于当前历史则检查失败；浅克隆先执行 `git fetch --unshallow`，CI 已配置 `fetch-depth: 0`。

维护顺序：

1. 运行同步检查，阅读对应源码 diff，核对 API、命令、返回值、限制和关联路径是否仍准确。
2. 修改内容；中文正文按 humanizer-zh 校订，不改掉条件、否定、版本和确定程度。代码、命令、URL 和数据不随文字润色改写。
3. 执行相关教程和测试。如果代码已经提交且复核完成，手动把该页 codeCommit 改为这次代码 SHA；不需要为未变化的页面统一刷新。
4. 若相关实现仍未提交，保留原基线和差异提示，待代码提交后复核再更新。不能填写尚不存在的“未来文档提交”，也不能用自动盖章掩盖差异。

纯粹修改文字、链接或排版，不必提升代码基线。修改关联路径时，仍需确认它们覆盖文章实际依赖；检查器无法从正文自动推断遗漏的源码。

## 构建时填写版本

会变化的安装版本和工具链说明使用 `docs` 占位符。构建时从配置读取，写进最终 HTML、代码复制内容和搜索索引；浏览器不查询版本，也不动态渲染文章。

| 变量后缀 | 数据来源 |
| --- | --- |
| `packageVersion` | 以本页 codeCommit 执行 `go list -m -json github.com/Cyenoch/cordis@commit`，取可获取的精确版本 |
| `goVersion`、`wazeroVersion` | 本页 codeCommit 对应的 go.mod |
| `codeCommit` | 本页人工复核的 front matter |

标记格式为 `{{docs.packageVersion}}`，可用于正文、行内代码及代码块；不用于 front matter、标题或链接地址。示例是作者指南中的 `go get` 命令。未知变量、配置缺失或基线版本无法获取会使构建失败，不回退到 latest，也不编造 pseudo-version。本地构建同样需要 Go 和模块查询所需网络；同一次构建复用相同 commit 的查询结果。

修改 Node、Bun 或站点依赖时，只改各自配置和锁文件。框架版本变化仍需先复核文章，再更新 codeCommit；其余版本文本自动跟随该基线。历史验收的 Go 版本、性能数字、保留 schema 标识以及示例包的 `1.0.0` 是事实或业务数据，不能自动替换。

`docs/*.md` 不在构建时回写。直接在 GitHub 阅读时，用页面 codeCommit 替换安装命令的版本标记；工具链和依赖值查看表中配置来源。最终可复制命令以构建后的文档站为准。

## 教程怎么验证

`bun run docs:examples` 等价于：

```sh
go test -tags=integration -race ./conformance -run '^TestDocumentationTutorials$' -count=1 -timeout=3m
```

测试直接读取三篇实操里的 `main.go`、生成教程的 JSON 契约和“预期输出”代码块，在新的临时 module 中 replace 到当前检出，再运行。生成教程用当前 CLI 生成真实代码；每个程序以 `go run -race` 执行，输出必须与文章一致。它也包含在 `bash conformance/run.sh` / `conformance/run.ps1` 中，由既有跨平台 CI 执行。

教程以 builtin 或直接 access API 解释机制，不冒充外部进程验证。Native/WASM 的构建、签名包、取消、停止和恢复继续由既有外部验收负责。新增教程时将其纳入同一入口，不复制一份“差不多”的程序充当文档验证。

## GitHub Pages 首次设置

仓库 **Settings → Pages → Build and deployment → Source** 选择 **GitHub Actions**。配置入口：[Cyenoch/cordis Pages settings](https://github.com/Cyenoch/cordis/settings/pages)。

也可以由有仓库管理权限的维护者执行一次：

```sh
gh api --method POST repos/Cyenoch/cordis/pages -f build_type=workflow
```

已存在 Pages 站点时使用 `PUT` 更新 `build_type`，不重复创建。

## 自动部署规则

- 推送到 `main`：锁文件安装、文档测试、基线检查、生产构建与链接校验成功后，上传静态产物并部署；相关源码差异作为 warning 保留。
- Pull request：运行相同构建和校验，不申请 Pages 写权限、不发布。
- 手动执行：在 [Documentation workflow](https://github.com/Cyenoch/cordis/actions/workflows/docs.yml) 选择 `main` 运行；其他分支只构建。
- 发布 job 使用 GitHub 提供的 Pages / OIDC 权限，并以只读 contents 权限核对 main HEAD；不创建 `gh-pages` 分支，不保存 PAT。
- 构建失败不会进入部署；同一 ref 的构建与部署整条流水线使用同一并发组，不中断正在执行的发布。GitHub 不保证排队顺序，不能把并发组当作严格的提交顺序队列；部署前还要核对 main HEAD。

目标地址是 **[cyenoch.github.io/cordis/](https://cyenoch.github.io/cordis/)**。首次必须将工作流和源码提交、推送到 `main` 并实际运行成功，才能称为已上线；启用 Pages 或本地构建通过不等于部署完成。

## 子路径与迁移

当前仓库使用 `/cordis/` 项目子路径。在 `site/astro.config.mjs` 配置 `site` / `base`，构建期 Markdown 链接与检查器使用相同子路径。若迁移仓库或改为自定义域名，需要一起更新这些位置及工作流文档，再执行完整构建；不要只改首页链接。
