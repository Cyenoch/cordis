import { defineConfig } from 'astro/config'
import { unified } from '@astrojs/markdown-remark'
import starlight from '@astrojs/starlight'
import { documentationMarkdown } from './markdown.mjs'

export default defineConfig({
  site: 'https://cyenoch.github.io',
  base: '/cordis',
  trailingSlash: 'always',
  markdown: { processor: unified({ remarkPlugins: [documentationMarkdown] }) },
  integrations: [
    starlight({
      title: 'Cordis',
      description: 'Go 插件框架：从第一个插件到独立宿主。',
      disable404Route: true,
      favicon: '/favicon.svg',
      logo: { src: './src/assets/mark.svg' },
      locales: { root: { label: '简体中文', lang: 'zh-CN' } },
      customCss: [
        '@fontsource-variable/manrope',
        '@fontsource-variable/jetbrains-mono',
        './src/styles/custom.css',
      ],
      components: { Hero: './src/components/Hero.astro' },
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/Cyenoch/cordis' },
      ],
      editLink: { baseUrl: 'https://github.com/Cyenoch/cordis/edit/main/' },
      tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
      markdown: { processedDirs: ['../docs/'] },
      expressiveCode: {
        themes: ['github-light', 'github-dark'],
        styleOverrides: {
          borderRadius: '0.75rem',
          codeFontFamily: 'var(--sl-font-mono)',
          codeFontSize: '0.8125rem',
        },
      },
      sidebar: [
        {
          label: '开始使用',
          items: [
            { label: '认识 Cordis', slug: 'index' },
            { label: '快速开始', slug: 'getting-started' },
            { label: '核心概念', slug: 'concepts' },
          ],
        },
        {
          label: '动手开发',
          items: [
            { label: '编写插件', slug: 'plugin-author' },
            { label: '接入宿主', slug: 'host-guide' },
            { label: 'CLI 命令', slug: 'cli' },
          ],
        },
        {
          label: '实操教程',
          items: [
            { label: '组合实操', slug: 'composition-tutorial' },
            { label: '生成服务', slug: 'generated-service' },
          ],
        },
        {
          label: '框架指南',
          items: [
            { label: '组合、实例与清理', slug: 'composition' },
            { label: '数据契约与代码生成', slug: 'contracts' },
            { label: '资源清理', slug: 'resources' },
            { label: '外部插件包与签名', slug: 'packages' },
            { label: 'Native 运行时', slug: 'runtimes' },
            { label: '中断恢复', slug: 'recovery' },
          ],
        },
        {
          label: '参考与运维',
          items: [
            { label: 'Go API 导航', slug: 'api' },
            { label: '故障排查', slug: 'troubleshooting' },
            { label: '上线与维护', slug: 'operations' },
            { label: '会话协议', slug: 'protocol' },
            { label: '生命周期验收', slug: 'lifecycle-acceptance' },
            { label: '回归覆盖', slug: 'regressions' },
            { label: '性能测量', slug: 'performance' },
          ],
        },
      ],
    }),
  ],
})
