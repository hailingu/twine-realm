/** 从同一受控模板生成文档摘要、版本绑定和发布清单；检查模式不改写文件。 */
import { createHash } from 'node:crypto'
import { readFile, readdir, mkdir, writeFile, realpath } from 'node:fs/promises'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

/** 首个版本的发布材料固定在仓库中，命令不接受外部路径或任意执行入口。 */
const directory = fileURLToPath(new URL('../server/internal/engineering/assets/go-net-http-v1/', import.meta.url))
const template = join(directory, '_template')
const digest = value => 'sha256:' + createHash('sha256').update(value).digest('hex')
const json = value => JSON.stringify(value, null, 2) + '\n'

/** 按字符串的固定字典序发布路径，避免主机区域设置改变摘要。 */
function comparePaths(left, right) {
  if (left === right) return 0
  return left < right ? -1 : 1
}

/** 只扫描本包普通文件，拒绝符号链接；点文件同样进入发布证据。 */
async function filesIn(directory) {
  const result = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const target = join(directory, entry.name)
    if (entry.isSymbolicLink()) throw new Error('工程包不得包含符号链接')
    if (entry.isDirectory()) result.push(...await filesIn(target))
    else if (entry.isFile()) result.push(target)
    else throw new Error('工程包包含不支持的文件类型')
  }
  return result.sort(comparePaths)
}

/** 原始模板摘要排除自引用元数据，保留真正导出后的路径。 */
async function templateOrigin(names) {
  const origin = []
  for (const name of names) {
    const path = relative(template, name).replaceAll('\\', '/')
    if (path === '.twine/profile.json' || path === '.twine/engineering.lock.json') continue
    origin.push({ path: path === 'go.mod.template' ? 'go.mod' : path, digest: digest(await readFile(name)) })
  }
  return origin
}

/** 检查模式逐字节核实，发布模式只更新本包受控产物。 */
async function applyOutputs(outputs, check) {
  for (const [path, data] of outputs) {
    if (check) {
      if (await readFile(path, 'utf8') !== data) throw new Error('工程包摘要或发布清单已过期，请更新首发材料；已发布版本须新增版本而非原地改义')
    } else {
      await mkdir(join(path, '..'), { recursive: true })
      await writeFile(path, data)
    }
  }
}

/** 生成只固定规范与兼容组合的锁，不把用户后续业务源码误当不可修改模板。 */
export async function generateServiceProfile(check = false) {
  const source = JSON.parse(await readFile(join(directory, 'profile.source.json'), 'utf8'))
  const names = await filesIn(template)
  const origin = await templateOrigin(names)
  const documents = []
  for (const document of source.documents) documents.push({ ...document, digest: digest(await readFile(join(template, document.path))) })
  const profile = { ...source, template: { ...source.template, digest: digest(json(origin)) }, documents }
  const profileData = json(profile)
  const lockData = json({ schemaVersion: 1, profile: { id: source.id, version: source.version, digest: digest(profileData) } })
  const outputs = new Map([[join(template, '.twine/profile.json'), profileData], [join(template, '.twine/engineering.lock.json'), lockData]])
  const publicationNames = new Set([...names, ...outputs.keys()])
  const files = []
  for (const name of [...publicationNames].sort(comparePaths)) {
    const data = outputs.get(name) ?? await readFile(name)
    const path = relative(template, name).replaceAll('\\', '/')
    files.push({ path: path === 'go.mod.template' ? 'go.mod' : path, source: path, digest: digest(data), size: Buffer.byteLength(data) })
  }
  outputs.set(join(directory, 'release.json'), json({ schemaVersion: 1, id: source.id, version: source.version, profileDigest: digest(profileData), files }))
  await applyOutputs(outputs, check)
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await generateServiceProfile(process.argv.includes('--check')); process.stdout.write('[SERVICE_PROFILE_OK] 工程包摘要与发布清单一致\n') }
  catch (error) { process.stderr.write(error.message + '\n'); process.exitCode = 1 }
}
