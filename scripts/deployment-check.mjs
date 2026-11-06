/** 只清理本次随机运行与构建安装共同拥有的业务容器和入口网络，生产API无此入口。 */
import { createHash } from 'node:crypto'
import { runCommand } from './sonar-gate.mjs'
import { parseRuntimeInstallation } from './runtime-check.mjs'
import { parseBuildInstallation } from './build-check.mjs'

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const ID = /^[0-9a-f]{64}$/
const DIGEST = /^sha256:[0-9a-f]{64}$/

/** @param options 本次初始化证据 @returns 按完整归属核实并删除一次性无数据业务资源。 */
export async function cleanupDeploymentResources({ runtimeInstallation, buildInstallation, env = process.env, execute = runCommand }) {
  if (!runtimeInstallation || !buildInstallation) return
  const runtime = parseRuntimeInstallation(JSON.stringify(runtimeInstallation))
  const build = parseBuildInstallation(JSON.stringify(buildInstallation))
  const dockerEnv = Object.fromEntries(['PATH', 'HOME', 'TMPDIR'].filter(key => env[key] !== undefined).map(key => [key, env[key]]))
  const call = async args => {
    const value = await execute('docker', ['--context', runtime.context, ...args], { env: dockerEnv, timeoutMs: 30000 })
    if (value.code !== 0 || Buffer.byteLength(value.output ?? '') > 1024 * 1024 || /warning:|\blevel=warn(?:ing)?\b/i.test(value.output ?? '')) throw new Error('本次业务部署清理未确认，保留原资源与运行材料')
    return value.output ?? ''
  }
  try {
    if (runtime.context !== build.environment.context || runtime.daemonId !== build.environment.daemonId || parseJSON(await call(['info', '--format', '{{json .ID}}'])) !== runtime.daemonId) throw new Error('业务测试清理的原daemon或安装不一致')
    for (const kind of ['container', 'network']) await cleanupKind(kind, runtime, build, call)
  } catch (error) {
    const failure = error instanceof Error ? error : new Error('业务测试清理返回无法核实的错误')
    failure.preserveTemporary = true
    throw failure
  }
}

/** @param kind 受控资源类型 @param runtime 原运行安装 @param build 原构建安装 @param call 固定context命令 @returns 逐个核实后清理。 */
async function cleanupKind(kind, runtime, build, call) {
  const args = [kind, 'ls', '--no-trunc', '--filter', `label=io.twine-realm.deploy.installation=${runtime.installationId}`, '--format', '{{json .}}']
  if (kind === 'container') args.push('--all')
  const rows = (await call(args)).trim().split('\n').filter(Boolean)
  if (rows.length > 500) throw new Error('本次业务部署资源超过核实上限，保留现场')
  for (const line of rows) {
    await cleanupObject(kind,parseJSON(line),runtime,build,call)
  }
}

/** @param kind 资源类型 @param row 单一名单 @param runtime 原运行安装 @param build 原构建安装 @param call 固定context命令 @returns 一个对象完整核实后才删除。 */
async function cleanupObject(kind,row,runtime,build,call) {
    if (!ID.test(row.ID)) throw new Error('本次业务部署名单身份不完整')
    const values = parseJSON(await call([kind, 'inspect', row.ID]))
    if (!Array.isArray(values) || values.length !== 1) throw new Error('业务测试资源无法唯一核实')
    const value = values[0]
    const labels = kind === 'container' ? value.Config?.Labels : value.Labels
    const { role, prefix } = ownedIdentity(labels, runtime)
    if ((value.Id ?? value.ID) !== row.ID) throw new Error('业务测试资源真实ID不一致')
    if (kind === 'container') await verifyContainer(value, role, prefix, build, call)
    else verifyNetwork(value, role, prefix)
    await call(kind === 'container' ? [kind, 'rm', '--force', row.ID] : [kind, 'rm', row.ID])
}

/** @param labels 实际资源标签 @param runtime 原运行安装 @returns 完整归属派生的固定名称。 */
function ownedIdentity(labels, runtime) {
  if (!labels || labels['io.twine-realm.deploy.installation'] !== runtime.installationId || !UUID.test(labels['io.twine-realm.deploy.project']) || !UUID.test(labels['io.twine-realm.deploy.service']) || !UUID.test(labels['io.twine-realm.deploy.resource']) || !DIGEST.test(labels['io.twine-realm.deploy.runtime'])) throw new Error('业务测试资源完整归属不匹配，原对象保留')
  const prefix = 'tr-' + createHash('sha256').update(JSON.stringify([runtime.installationId, labels['io.twine-realm.deploy.project'], labels['io.twine-realm.deploy.service']])).digest('hex').slice(0, 24)
  return { prefix, role: labels['io.twine-realm.deploy.role'] }
}

/** @param value 原容器 @param role 受控角色 @param prefix 确定前缀 @param build 原构建安装 @param call 只读核实端口 @returns 镜像与运行资源共同归属本次测试。 */
async function verifyContainer(value, role, prefix, build, call) {
  if (!['application', 'migration'].includes(role) || value.Name !== '/' + prefix + (role === 'application' ? '-service' : '-migration') || !DIGEST.test(value.Image) || value.Config?.Image !== value.Image) throw new Error('业务测试容器名称或镜像不匹配')
  const images = parseJSON(await call(['image', 'inspect', value.Image]))
  if (!Array.isArray(images) || images.length !== 1 || images[0].Id !== value.Image || images[0].Config?.Labels?.['io.twine-realm.build.installation'] !== build.installationId) throw new Error('业务测试容器使用了其他构建安装的镜像，保留现场')
}

/** @param value 入口网络 @param role 原角色 @param prefix 确定名称 @returns 只删除本项空入口网络，不删除数据库网络或数据卷。 */
function verifyNetwork(value, role, prefix) {
  if (role !== 'ingress' || value.Name !== prefix + '-http' || value.Driver !== 'bridge' || value.Scope !== 'local' || value.Internal || !value.EnableIPv4 || value.EnableIPv6 || Object.keys(value.Containers ?? {}).length !== 0 || value.Options?.['com.docker.network.bridge.host_binding_ipv4'] !== '127.0.0.1') throw new Error('业务入口网络配置、归属或使用状态不匹配，原对象保留')
}

/** @param value 原始JSON @returns 安全解析，不将外部片段嵌入错误。 */
function parseJSON(value) { try { return JSON.parse(value) } catch { throw new Error('业务测试资源返回无效JSON，原对象保留') } }
