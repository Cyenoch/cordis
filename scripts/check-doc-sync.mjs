import { execFileSync } from 'node:child_process'
import { readdir, readFile, stat } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseFrontmatter } from '@astrojs/markdown-remark'

export async function checkDocSync(root) {
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  const existsInGit = (commit, path) => {
    try { git('cat-file', '-e', `${commit}:${path}`); return true } catch { return false }
  }
  const reports = []
  const failures = []
  for (const name of (await readdir(resolve(root, 'docs'))).filter((name) => name.endsWith('.md')).sort()) {
    const file = `docs/${name}`
    try {
      const { frontmatter } = parseFrontmatter(await readFile(resolve(root, file), 'utf8'))
      const { codeCommit: commit, codePaths: paths } = frontmatter
      if (typeof commit !== 'string' || !/^[a-f0-9]{40}$/.test(commit)) {
        throw new Error('codeCommit must be a full 40-character commit SHA')
      }
      try {
        git('cat-file', '-e', `${commit}^{commit}`)
        git('merge-base', '--is-ancestor', commit, 'HEAD')
      } catch {
        throw new Error(`codeCommit ${commit} is unavailable or not an ancestor of HEAD; fetch full history before checking`)
      }
      if (!Array.isArray(paths) || paths.length === 0) throw new Error('codePaths must contain related repository files or directories')
      for (const path of paths) {
        if (typeof path !== 'string' || !/^[A-Za-z0-9_.][A-Za-z0-9_./-]*$/.test(path) || path.split('/').some((part) => !part || part === '.' || part === '..')) {
          throw new Error(`invalid codePaths entry: ${JSON.stringify(path)}; use literal repository-relative paths, not globs`)
        }
        if (!existsInGit(commit, path) && !existsInGit('HEAD', path) && !(await stat(resolve(root, path)).catch(() => null))) {
          throw new Error(`codePaths entry does not exist at baseline or in this checkout: ${path}`)
        }
      }
      const pathspecs = paths.map((path) => `:(literal)${path}`)
      const changed = new Set([
        ...git('diff', '--name-only', '--no-renames', '-z', commit, '--', ...pathspecs).split('\0'),
        ...git('diff', '--cached', '--name-only', '--no-renames', '-z', commit, '--', ...pathspecs).split('\0'),
        ...git('ls-files', '--others', '--exclude-standard', '-z', '--', ...pathspecs).split('\0'),
      ].filter(Boolean))
      reports.push({ file, commit, paths, changed: [...changed].sort() })
    } catch (error) {
      failures.push(`${file}: ${error.message}`)
    }
  }
  if (failures.length) throw new Error(failures.join('\n'))
  return reports
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const reports = await checkDocSync(fileURLToPath(new URL('../', import.meta.url)))
    const escape = (text) => text.replaceAll('%', '%25').replaceAll('\r', '%0D').replaceAll('\n', '%0A')
    for (const report of reports.filter((report) => report.changed.length)) {
      const message = `Code changed since ${report.commit.slice(0, 7)}; review ${report.file}: ${report.changed.join(', ')}`
      console.log(process.env.GITHUB_ACTIONS ? `::warning file=${escape(report.file)}::${escape(message)}` : message)
    }
    const affected = reports.filter((report) => report.changed.length).length
    console.log(`Documentation baselines: ${reports.length} pages checked, ${affected} need review. Matching paths do not prove behavioral correctness.`)
  } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}
