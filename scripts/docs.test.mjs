import assert from 'node:assert/strict'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { createMarkdownProcessor } from '@astrojs/markdown-remark'
import { documentationMarkdown } from '../site/markdown.mjs'
import { checkDocs } from './check-docs.mjs'

test('repository Markdown renders working Pages links without a duplicate title', async () => {
  const processor = await createMarkdownProcessor({ remarkPlugins: [documentationMarkdown] })
  const { code } = await processor.render([
    '# A page',
    '[guide](composition.md#scope)',
    '[home](index.md)',
    '[evidence](../openspec/changes/archive/2026-10-28-build-go-plugin-framework/tasks.md)',
    '## A section',
    '```go',
    'fmt.Println("composition.md")',
    '```',
  ].join('\n\n'))
  assert.match(code, /href="\/cordis\/composition\/#scope"/)
  assert.match(code, /href="\/cordis\/"/)
  assert.match(code, /href="https:\/\/github.com\/Cyenoch\/cordis\/blob\/main\/openspec\//)
  assert.doesNotMatch(code, /<h1/)
  assert.match(code, /<h2[^>]*>A section/)
  assert.match(code, /composition\.md/)
})

test('production checker rejects missing anchors and project-base escapes', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'cordis-doc-links-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  await writeFile(join(directory, 'index.html'), '<h1 id="start">Start</h1><a href="/cordis/#start">Read</a>')
  assert.equal((await checkDocs(directory)).pages, 1)

  await writeFile(join(directory, 'index.html'), '<a href="/cordis/#missing">Read</a><script src="/app.js"></script>')
  await assert.rejects(checkDocs(directory), (error) => {
    assert.match(error.message, /missing anchor/)
    assert.match(error.message, /link escapes \/cordis\//)
    return true
  })

  await writeFile(join(directory, 'index.html'), '<pre>go get example.com/module@{{docs.packageVersion}}</pre>')
  await assert.rejects(checkDocs(directory), /unresolved documentation variable/)
})

test('production checker rejects unresolved variables in rendered HTML attributes', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'cordis-doc-attributes-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const processor = await createMarkdownProcessor({ remarkPlugins: [documentationMarkdown] })
  for (const source of [
    '![{{docs.unknown}}](https://example.com/logo.png)',
    '[Link](https://example.com/ "{{docs.unknown}}")',
    '<span title="{{docs.unknown}}">Label</span>',
  ]) {
    const { code } = await processor.render(source)
    await writeFile(join(directory, 'index.html'), code)
    await assert.rejects(checkDocs(directory), /unresolved documentation variable/, source)
  }
})
