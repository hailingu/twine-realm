/** 核实一次性运行测试的安装身份；清理仅针对本次全新初始化的隔离命名空间。 */
import { runCommand } from './sonar-gate.mjs'

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const CONTEXT = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/
const DAEMON = /^[A-Za-z0-9:._-]{1,128}$/
const CONTENT_ID = /^[0-9a-f]{64}$/
const VOLUME = /^tr-[0-9a-f]{32}-data$/

/** 初始化响应只含非敏感身份，不能把后来被改写的文件当作清理授权。 */
export function parseRuntimeInstallation(output) {
  const value = parseJSON(output)
  if (!value || Object.keys(value).length !== 5 || value.schemaVersion !== 1 || value.adapter !== 'file-secrets-v1' || typeof value.installationId !== 'string' || typeof value.context !== 'string' || typeof value.daemonId !== 'string' || !UUID.test(value.installationId) || !CONTEXT.test(value.context) || !DAEMON.test(value.daemonId)) throw new Error('临时运行安装报告无效，未开始资源测试')
  return Object.freeze(value)
}

/** 聚合错误仍保留现场标记，调用者不得在绑定仍可能存在时删除秘密文件。 */
export function requiresPreservation(error) {
  return error?.preserveTemporary === true || (error instanceof AggregateError && error.errors.some(requiresPreservation))
}

/** 只供本次受信测试清理使用，生产API没有删除卷入口。 */
export async function cleanupRuntimeResources({ installation, env = process.env, execute = runCommand }) {
  if (!installation) return
  installation = parseRuntimeInstallation(JSON.stringify(installation))
  const dockerEnv = {}
  for (const key of ['PATH', 'HOME', 'TMPDIR']) if (env[key] !== undefined) dockerEnv[key] = env[key]
  const call = async args => {
    const result = await execute('docker', ['--context', installation.context, ...args], { env: dockerEnv, timeoutMs: 30000 })
    const warning = (result.output ?? '').split('\n').some(line => /^warning:/i.test(line) || /\blevel=warn(?:ing)?\b/i.test(line))
    if (result.code !== 0 || Buffer.byteLength(result.output ?? '') > 1024 * 1024 || warning) throw new Error('本次运行测试资源清理未确认，保留私有运行材料')
    return result.output ?? ''
  }
  try {
    const daemonID = parseJSON(await call(['info', '--format', '{{json .ID}}']))
    if (daemonID !== installation.daemonId) throw new Error('清理时Docker身份已改变，保留原资源和私有材料')
    const failures = []
    for (const kind of ['container', 'volume', 'network']) {
      try { await cleanupKind(kind, installation, call) } catch (error) { failures.push(error) }
    }
    if (failures.length) throw new AggregateError(failures, '本次运行测试资源未全部清理，需保留私有材料')
  } catch (error) {
    const failure = error instanceof Error ? error : new Error('本次运行清理出现无法核实的错误')
    failure.preserveTemporary = true
    throw failure
  }
}

/** 名单来自精确本次安装标签；删除前再核实实际对象，不按名称前缀批量删除。 */
async function cleanupKind(kind, installation, call) {
  const args = [kind, 'ls', '--filter', `label=io.twine-realm.installation=${installation.installationId}`, '--format', '{{json .}}']
  if (kind === 'container') args.push('--all')
  if (kind !== 'volume') args.push('--no-trunc')
  const output = await call(args)
  const lines = output.trim() ? output.trim().split('\n') : []
  if (lines.length > 500) throw new Error('本次测试资源数超出核实上限，保留现场')
  for (const line of lines) {
    const row = parseJSON(line)
    const identity = kind === 'volume' ? row.Name : row.ID
    if (!(kind === 'volume' ? VOLUME : CONTENT_ID).test(identity)) throw new Error('清理名单包含无效资源身份，保留现场')
    const values = parseJSON(await call([kind, 'inspect', identity]))
    if (!Array.isArray(values) || values.length !== 1) throw new Error('清理对象无法唯一核实，保留现场')
    verifyOwned(kind, identity, values[0], installation)
    await call(kind === 'container' ? [kind, 'rm', '--force', identity] : [kind, 'rm', identity])
  }
}

/** 当前安装随机身份与完整归属标签共同证明一次性测试所有权，外来对象保持原样。 */
function verifyOwned(kind, identity, value, installation) {
  const labels = kind === 'container' ? value.Config?.Labels : value.Labels
  if (!labels || labels['io.twine-realm.installation'] !== installation.installationId || labels['io.twine-realm.adapter'] !== 'mysql-compose-v1' || !UUID.test(labels['io.twine-realm.resource']) || !UUID.test(labels['io.twine-realm.project']) || !UUID.test(labels['io.twine-realm.component'])) throw new Error('清理对象归属不匹配，原对象保留')
  if (kind === 'volume') {
    if (value.Name !== identity || value.Driver !== 'local' || Object.keys(value.Options ?? {}).length !== 0) throw new Error('清理数据卷身份或驱动不匹配，原对象保留')
  } else if (value.Id !== identity && value.ID !== identity) throw new Error('清理对象内容身份已改变，原对象保留')
}

/** 解析失败不回显外部输出，即使Docker或初始化结果被污染也不泄露片段。 */
function parseJSON(value) {
  try { return JSON.parse(value) } catch { throw new Error('运行测试工具返回无法核实的JSON，停止并保留现场') }
}
