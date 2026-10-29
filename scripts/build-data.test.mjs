import assert from 'node:assert/strict'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { createMarkdownProcessor } from '@astrojs/markdown-remark'
import { loadCodeVersions } from '../site/build-data.mjs'
import { documentationMarkdown } from '../site/markdown.mjs'

test('documentation renders checkout variables in prose and commands', async (t) => {
  t.mock.method(console, 'error', () => {})
  const processor = await createMarkdownProcessor({ remarkPlugins: [[documentationMarkdown, {
    buildData: { modulePath: 'example.com/cordis', goVersion: '1.27.3' },
  }]] })
  const { code } = await processor.render('# Guide\n\nGo {{docs.goVersion}}.\n\n```sh\ngo mod edit -require={{docs.modulePath}}@v0.0.0\n```')
  assert.match(code, /Go 1\.27\.3/)
  assert.match(code, /example\.com\/cordis@v0\.0\.0/)
  assert.doesNotMatch(code, /\{\{docs\./)
  for (const text of ['{{docs.unknown}}', '{{docs.goVersion', '# {{docs.goVersion}}', '[link](https://example.com/{{docs.goVersion}})']) {
    await assert.rejects(processor.render(text))
  }
})

test('documentation reads the current local module without a release lookup', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cordis-doc-versions-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  await writeFile(join(root, 'go.mod'), 'module example.com/cordis\n\ngo 1.27.3\n')
  assert.deepEqual(await loadCodeVersions(root), { modulePath: 'example.com/cordis', goVersion: '1.27.3' })
  await writeFile(join(root, 'go.mod'), 'module example.com/cordis\n')
  await assert.rejects(loadCodeVersions(root), /Missing module or Go version/)
})
