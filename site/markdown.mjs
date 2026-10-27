export function documentationMarkdown() {
  return (tree) => {
    // Starlight renders the page title; Markdown keeps its H1 for GitHub readers.
    const title = tree.children.findIndex((node) => node.type === 'heading' && node.depth === 1)
    if (title !== -1) tree.children.splice(title, 1)

    function visit(node) {
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
  }
}
