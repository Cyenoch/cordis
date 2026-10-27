import { execFile } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { promisify } from 'node:util'

const execute = promisify(execFile)

export async function loadCodeVersions(root, commit) {
  if (typeof commit !== 'string' || !/^[a-f0-9]{40}$/.test(commit)) {
    throw new Error('Documentation build variables require the page codeCommit')
  }
  const { stdout: mod } = await execute('git', ['show', `${commit}:go.mod`], { cwd: root })
  const module = /^module\s+(\S+)$/m.exec(mod)?.[1]
  const goVersion = /^go\s+(\S+)$/m.exec(mod)?.[1]
  const wazeroVersion = /^(?:require\s+|\s*)github\.com\/tetratelabs\/wazero\s+(\S+)(?:\s+\/\/[^\n]*)?$/m.exec(mod)?.[1]
  if (!module || !goVersion || !wazeroVersion) throw new Error(`Missing version declarations in ${commit}:go.mod`)
  const directory = await mkdtemp(join(tmpdir(), 'cordis-doc-version-'))
  try {
    // Query outside the checkout so its module and replace directives cannot mask publication.
    const { stdout } = await execute('go', ['list', '-m', '-json', `${module}@${commit}`], {
      cwd: directory,
      env: { ...process.env, GOWORK: 'off' },
      timeout: 120000,
    })
    const result = JSON.parse(stdout)
    if (result.Path !== module || !/^v\d+\.\d+\.\d+(?:[-+].*)?$/.test(result.Version ?? '') || (result.Origin?.Hash && result.Origin.Hash !== commit)) {
      throw new Error(`Go returned an unexpected module version for ${module}@${commit}`)
    }
    return { codeCommit: commit, packageVersion: result.Version, goVersion, wazeroVersion }
  } catch (error) {
    throw new Error(`Cannot resolve documentation baseline ${commit} to an available Go module version: ${error.message}`, { cause: error })
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
}
