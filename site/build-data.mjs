import { readFile } from 'node:fs/promises'
import { join } from 'node:path'

export async function loadCodeVersions(root) {
  const mod = await readFile(join(root, 'go.mod'), 'utf8')
  const modulePath = /^module\s+(\S+)$/m.exec(mod)?.[1]
  const goVersion = /^go\s+(\S+)$/m.exec(mod)?.[1]
  if (!modulePath || !goVersion) throw new Error('Missing module or Go version in go.mod')
  return { modulePath, goVersion }
}
