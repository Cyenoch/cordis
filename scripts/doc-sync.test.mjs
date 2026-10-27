import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { checkDocSync } from './check-doc-sync.mjs'

test('baselines report committed, staged, unstaged, deleted and new related sources, but not unrelated edits', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cordis-doc-sync-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  git('init', '-q')
  await mkdir(join(root, 'docs'))
  await mkdir(join(root, 'src'))
  for (const file of ['committed.go', 'staged.go', 'working.go', 'deleted.go']) {
    await writeFile(join(root, 'src', file), 'package source\n')
  }
  await writeFile(join(root, 'unrelated.go'), 'package unrelated\n')
  git('add', '.')
  git('-c', 'user.name=Docs Test', '-c', 'user.email=docs@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'baseline')
  const baseline = git('rev-parse', 'HEAD')
  const page = (commit = baseline, path = 'src') => `---\ncodeCommit: ${commit}\ncodePaths:\n  - ${path}\n---\n# Guide\n`
  await writeFile(join(root, 'docs', 'guide.md'), page())
  assert.deepEqual((await checkDocSync(root))[0].changed, [])
  await writeFile(join(root, 'unrelated.go'), 'package changed\n')
  assert.deepEqual((await checkDocSync(root))[0].changed, [])
  await writeFile(join(root, 'src', 'committed.go'), 'package committed\n')
  git('add', 'src/committed.go')
  git('-c', 'user.name=Docs Test', '-c', 'user.email=docs@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'source change')
  await writeFile(join(root, 'src', 'staged.go'), 'package staged\n')
  git('add', 'src/staged.go')
  await writeFile(join(root, 'src', 'working.go'), 'package working\n')
  await rm(join(root, 'src', 'deleted.go'))
  await writeFile(join(root, 'src', 'new.go'), 'package added\n')
  assert.deepEqual((await checkDocSync(root))[0].changed, [
    'src/committed.go', 'src/deleted.go', 'src/new.go', 'src/staged.go', 'src/working.go',
  ])

  await writeFile(join(root, 'src', 'staged.go'), 'package source\n')
  await writeFile(join(root, 'docs', 'guide.md'), page(baseline, 'src/staged.go'))
  assert.deepEqual((await checkDocSync(root))[0].changed, ['src/staged.go'])

  await writeFile(join(root, 'docs', 'guide.md'), page(baseline, 'src/deleted.go'))
  assert.deepEqual((await checkDocSync(root))[0].changed, ['src/deleted.go'])
  await writeFile(join(root, 'docs', 'guide.md'), page(baseline, 'src/new.go'))
  assert.deepEqual((await checkDocSync(root))[0].changed, ['src/new.go'])

  for (const [content, message] of [
    ['# No metadata', /codeCommit must/],
    [page('f'.repeat(40)), /unavailable or not an ancestor/],
    [page(baseline, '../outside'), /invalid codePaths/],
    [page(baseline, 'src/*.go'), /invalid codePaths/],
    [page(baseline, 'missing.go'), /does not exist/],
  ]) {
    await writeFile(join(root, 'docs', 'guide.md'), content)
    await assert.rejects(checkDocSync(root), message)
  }

  await writeFile(join(root, 'docs', 'guide.md'), page(git('rev-parse', 'HEAD')))
  git('checkout', '-q', '--detach', baseline)
  await assert.rejects(checkDocSync(root), /unavailable or not an ancestor/)
})
