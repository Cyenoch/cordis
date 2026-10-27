import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'
import { createMarkdownProcessor } from '@astrojs/markdown-remark'
import { loadCodeVersions } from '../site/build-data.mjs'
import { documentationMarkdown } from '../site/markdown.mjs'

test('version rendering follows each reviewed commit in prose and commands, without rewriting historical facts', async (t) => {
  t.mock.method(console, 'error', () => {})
  const first = 'a'.repeat(40)
  const second = 'b'.repeat(40)
  const processor = await createMarkdownProcessor({ remarkPlugins: [[documentationMarkdown, { buildData: {
    [first]: { packageVersion: 'v0.3.1', goVersion: '1.27.3' },
    [second]: { packageVersion: 'v0.4.0', goVersion: '1.28.0' },
  } }]] })
  const markdown = [
    '# Guide',
    'Go {{docs.goVersion}}, `{{docs.packageVersion}}`.',
    '```sh',
    'go get example.com/module@{{docs.packageVersion}}',
    '```',
    'Historical run: Go 1.26.0, schema cordis-go.session/v1, example package 1.0.0.',
  ].join('\n\n')
  for (const [commit, expected] of [[first, 'v0.3.1'], [second, 'v0.4.0']]) {
    const { code } = await processor.render(markdown, { frontmatter: { codeCommit: commit } })
    assert.match(code, new RegExp(`example.com/module@${expected.replaceAll('.', '\\.')}`))
    assert.match(code, /Historical run: Go 1\.26\.0, schema cordis-go\.session\/v1, example package 1\.0\.0/)
    assert.doesNotMatch(code, /\{\{docs\./)
  }
  for (const content of ['{{docs.unknown}}', '{{docs.goVersion', '# {{docs.goVersion}}', '[link](https://example.com/{{docs.goVersion}})']) {
    await assert.rejects(processor.render(content, { frontmatter: { codeCommit: first } }))
  }
  await assert.rejects(processor.render('{{docs.packageVersion}}'), /Unknown or unavailable/)
})

test('version resolution uses the baseline go.mod and a real local Go module proxy, never checkout replace or latest', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cordis-build-data-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  git('init', '-q')
  const module = `example.invalid/docs-${process.pid}-${Date.now()}`
  const mod = `module ${module}\n\ngo 1.27.3\n\nrequire github.com/tetratelabs/wazero v1.12.0\n`
  await writeFile(join(root, 'go.mod'), mod)
  git('add', 'go.mod')
  git('-c', 'user.name=Docs Test', '-c', 'user.email=docs@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'baseline')
  const commit = git('rev-parse', 'HEAD')
  const directory = resolve(root, 'proxy', module, '@v')
  await mkdir(directory, { recursive: true })
  const version = 'v0.3.1'
  const info = JSON.stringify({ Version: version, Time: '2026-10-27T00:00:00Z' })
  await writeFile(join(directory, `${commit}.info`), info)
  await writeFile(join(directory, `${version}.info`), info)
  await writeFile(join(directory, `${version}.mod`), mod)
  await writeFile(join(root, 'go.mod'), `module other.invalid/checkout\n\ngo 1.28.0\n`)
  const environment = {
    GOPROXY: `file://${join(root, 'proxy')}`, GOMODCACHE: join(root, 'modcache'),
    GOPRIVATE: '', GONOPROXY: '', GONOSUMDB: '', GOSUMDB: 'off',
  }
  for (const [key, value] of Object.entries(environment)) {
    const previous = process.env[key]
    t.after(() => { if (previous === undefined) delete process.env[key]; else process.env[key] = previous })
    process.env[key] = value
  }
  assert.deepEqual(await loadCodeVersions(root, commit), {
    codeCommit: commit, packageVersion: version, goVersion: '1.27.3', wazeroVersion: 'v1.12.0',
  })
  await assert.rejects(loadCodeVersions(root, 'bad'), /page codeCommit/)
  await writeFile(join(root, 'go.mod'), mod + '\n// New unpublished baseline.\n')
  git('add', 'go.mod')
  git('-c', 'user.name=Docs Test', '-c', 'user.email=docs@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'unpublished code')
  await assert.rejects(loadCodeVersions(root, git('rev-parse', 'HEAD')), /Cannot resolve documentation baseline/)
  await writeFile(join(root, 'go.mod'), 'module other.invalid/checkout\n\ngo 1.28.0\n')
  git('add', 'go.mod')
  git('-c', 'user.name=Docs Test', '-c', 'user.email=docs@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'missing baseline dependency')
  await assert.rejects(loadCodeVersions(root, git('rev-parse', 'HEAD')), /Missing version declarations/)
})
