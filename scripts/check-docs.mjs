import { readdir, readFile } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { relative, resolve } from 'node:path'
import { parse } from 'parse5'

const base = '/cordis/'

export async function checkDocs(directory) {
  const files = new Set()
  const pages = new Map()
  const failures = []

  async function collect(path) {
    for (const entry of await readdir(path, { withFileTypes: true })) {
      const full = resolve(path, entry.name)
      if (entry.isDirectory()) await collect(full)
      else files.add(relative(directory, full).replaceAll('\\', '/'))
    }
  }
  await collect(directory)

  for (const file of files) {
    if (!file.endsWith('.html')) continue
    const document = parse(await readFile(resolve(directory, file), 'utf8'))
    const ids = new Set()
    const links = []
    function visit(node) {
      const attributes = Object.fromEntries((node.attrs ?? []).map(({ name, value }) => [name, value]))
      if (attributes.id) ids.add(attributes.id)
      const keys = attributes.rel === 'canonical' ? ['src'] : ['href', 'src']
      for (const key of keys) {
        if (attributes[key]) links.push(attributes[key])
      }
      node.childNodes?.forEach(visit)
    }
    visit(document)
    pages.set(file, { ids, links })
  }

  for (const [file, page] of pages) {
    const location = new URL(`${base}${file}`, 'https://cyenoch.github.io')
    for (const link of page.links) {
      const url = new URL(link, location)
      if (url.origin !== location.origin) continue
      if (!url.pathname.startsWith(base)) {
        failures.push(`${file}: link escapes ${base}: ${link}`)
        continue
      }
      let target = decodeURIComponent(url.pathname.slice(base.length))
      if (target === '' || target.endsWith('/')) target += 'index.html'
      if (!files.has(target)) {
        failures.push(`${file}: missing target: ${link}`)
        continue
      }
      const hash = decodeURIComponent(url.hash.slice(1))
      if (hash && pages.has(target) && !pages.get(target).ids.has(hash)) {
        failures.push(`${file}: missing anchor: ${link}`)
      }
    }
  }
  if (failures.length) throw new Error(failures.join('\n'))
  return { pages: pages.size, files: files.size }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const directory = fileURLToPath(new URL('../site/dist/', import.meta.url))
  const result = await checkDocs(directory)
  console.log(`Documentation checked: ${result.pages} pages, ${result.files} files; links, anchors and /cordis/ assets resolve.`)
}
