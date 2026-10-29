import { fileURLToPath } from 'node:url'
import { loadCodeVersions } from './build-data.mjs'

export function documentationMarkdown({ buildData } = {}) {
  const root = fileURLToPath(new URL('../', import.meta.url))
  let versionData
  return async (tree) => {
    const variable = /\{\{docs\.([^}]+)\}\}/g
    const hasVariables = (node) => (
      ['text', 'inlineCode', 'code'].includes(node.type) && node.value.includes('{{docs.')
    ) || node.children?.some(hasVariables)
    let values
    if (hasVariables(tree)) {
      versionData ??= buildData ?? loadCodeVersions(root)
      values = await versionData
    }
    function visit(node) {
      if ((node.type === 'heading' && hasVariables(node)) || node.url?.includes('{{docs.')) {
        throw new Error('Documentation variables belong in prose and code, not headings or link addresses')
      }
      if (values && ['text', 'inlineCode', 'code'].includes(node.type)) {
        node.value = node.value.replace(variable, (_, name) => {
          if (!Object.hasOwn(values, name) || typeof values[name] !== 'string') {
            throw new Error(`Unknown or unavailable documentation variable: docs.${name}`)
          }
          return values[name]
        })
        if (node.value.includes('{{docs.')) throw new Error('Malformed documentation variable')
      }
      if (node.type === 'link') {
        if (node.url.startsWith('../openspec/')) {
          node.url = `https://github.com/Cyenoch/cordis/blob/main/${node.url.slice(3)}`
        } else if (/^(?:\.\/)?[\w-]+\.md(?:#.*)?$/.test(node.url)) {
          const [path, hash = ''] = node.url.replace(/^\.\//, '').split('#')
          const slug = path === 'index.md' ? '' : `${path.slice(0, -3)}/`
          node.url = `/cordis/${slug}${hash ? `#${hash}` : ''}`
        }
      }
      node.children?.forEach(visit)
    }
    visit(tree)
    // Starlight renders the page title; Markdown keeps its H1 for GitHub readers.
    const title = tree.children.findIndex((node) => node.type === 'heading' && node.depth === 1)
    if (title !== -1) tree.children.splice(title, 1)
  }
}
