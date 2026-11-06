/** 本次隔离构建检查只清理其随机安装的镜像tag；未确认的沙箱/卷保留现场。 */
import { runCommand } from './sonar-gate.mjs'

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const DIGEST = /^sha256:[0-9a-f]{64}$/
const CONTEXT = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/

/** @param output 本次初始化报告 @returns 不可变且不含秘密的安装证据。 */
export function parseBuildInstallation(output) {
  const value = parseJSON(output)
  const environment = value?.environment
  if (value?.schemaVersion !== 1 || value.adapter !== 'local-go-container-v1' || !UUID.test(value.installationId) || !CONTEXT.test(environment?.context) || !/^[A-Za-z0-9_.:-]{1,128}$/.test(environment?.daemonId) || !DIGEST.test(environment?.imageId) || !['amd64', 'arm64'].includes(environment?.architecture)) throw new Error('临时构建安装报告无效，未开始资源测试')
  return Object.freeze({ ...value, environment: Object.freeze({ ...environment }) })
}

/** @param options 本次冻结安装与受控Docker端口 @returns 只移除归属核实的本次候选tag。 */
export async function cleanupBuildResources({ installation, env = process.env, execute = runCommand }) {
  if (!installation) return
  installation = parseBuildInstallation(JSON.stringify(installation))
  const dockerEnv = Object.fromEntries(['PATH', 'HOME', 'TMPDIR'].filter(key => env[key] !== undefined).map(key => [key, env[key]]))
  const call = async args => {
    const result = await execute('docker', ['--context', installation.environment.context, ...args], { env: dockerEnv, timeoutMs: 30000 })
    if (result.code !== 0 || Buffer.byteLength(result.output ?? '') > 1024 * 1024 || /warning:|\blevel=warn(?:ing)?\b/i.test(result.output ?? '')) throw new Error('本次构建资源核实失败，保留现场')
    return result.output ?? ''
  }
  try {
    if (parseJSON(await call(['info', '--format', '{{json .ID}}'])) !== installation.environment.daemonId) throw new Error('构建清理时daemon已改变，保留现场')
    const filter = `label=io.twine-realm.build.installation=${installation.installationId}`
    for (const kind of ['container', 'volume']) {
      const args = [kind, 'ls', '--filter', filter, '--format', '{{json .}}']
      if (kind === 'container') args.push('--all')
      if ((await call(args)).trim()) throw new Error('构建沙箱或输出卷尚未确认收回，保留私有材料与资源')
    }
    const rows = (await call(['image', 'ls', '--no-trunc', '--filter', filter, '--format', '{{json .}}'])).trim().split('\n').filter(Boolean)
    if (rows.length > 500) throw new Error('本次构建镜像清理超过核实上限')
    for (const line of rows) await cleanupImage(parseJSON(line), installation, call)
  } catch (error) {
    const failure = error instanceof Error ? error : new Error('构建清理结果未确认')
    failure.preserveTemporary = true
    throw failure
  }
}

/** @param row 本安装镜像列表项 @param installation 冻结身份 @param call 有界命令 @returns 删除精确本次tag，保留其他引用。 */
async function cleanupImage(row, installation, call) {
  if (!DIGEST.test(row.ID)) throw new Error('构建镜像名单身份无效')
  const values = parseJSON(await call(['image', 'inspect', row.ID]))
  if (!Array.isArray(values) || values.length !== 1) throw new Error('本次构建镜像无法唯一核实')
  const value = values[0]; const labels = value.Config?.Labels
  if (value.Id !== row.ID || value.Os !== 'linux' || value.Architecture !== installation.environment.architecture || value.Config.User !== '65532:65532' || !labels || Object.keys(labels).length !== 4 || labels['io.twine-realm.build.installation'] !== installation.installationId || !UUID.test(labels['io.twine-realm.build.operation']) || !UUID.test(labels['io.twine-realm.build.target']) || !DIGEST.test(labels['io.twine-realm.build.input'])) throw new Error('本次构建镜像完整归属不匹配，保留现场')
  const tags = (value.RepoTags ?? []).filter(tag => /^tr-build-[0-9a-f]{32}-candidate:local$/.test(tag))
  if (tags.length !== 1) throw new Error('本次镜像tag无法唯一核实，保留其他引用')
  await call(['image', 'rm', tags[0]])
}

/** @param value 不信任的工具输出 @returns 有效JSON；错误不回显任何输入片段。 */
function parseJSON(value) { try { return JSON.parse(value) } catch { throw new Error('构建检查工具返回无效JSON，保留现场') } }
