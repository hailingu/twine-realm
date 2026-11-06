/** 业务清理核实双安装、真实ID和完整归属；任何外来对象、告警或失败都保留。 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { cleanupDeploymentResources } from './deployment-check.mjs'

const runtime = { schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000016', adapter: 'file-secrets-v1', context: 'test-local', daemonId: 'test-daemon' }
const build = { schemaVersion: 1, installationId: '20000000-0000-4000-8000-000000000016', adapter: 'local-go-container-v1', environment: { context: runtime.context, daemonId: runtime.daemonId, imageId: 'sha256:' + 'a'.repeat(64), architecture: 'amd64' } }
const projectId = '30000000-0000-4000-8000-000000000016'
const serviceId = '40000000-0000-4000-8000-000000000016'
const prefix = 'tr-' + createHash('sha256').update(JSON.stringify([runtime.installationId, projectId, serviceId])).digest('hex').slice(0, 24)
const baseLabels = { 'io.twine-realm.deploy.installation': runtime.installationId, 'io.twine-realm.deploy.project': projectId, 'io.twine-realm.deploy.service': serviceId, 'io.twine-realm.deploy.resource': '50000000-0000-4000-8000-000000000016', 'io.twine-realm.deploy.runtime': 'sha256:' + 'b'.repeat(64) }
const imageId = 'sha256:' + 'c'.repeat(64)

/** @param changes 单个故障 @returns 工具协议及调用记录，不执行Docker。 */
function fixture(changes = {}) {
  const calls = []
  const execute = async (_command, args, settings) => {
    calls.push({ args, settings })
    const [, , kind, action, id] = args
    if (changes.failed) return { code: 1, output: '' }
    if (changes.warning) return { code: 0, output: 'level=warning synthetic-warning' }
    if (changes.invalid) return { code: 0, output: 'synthetic-private-fragment' }
    if (kind === 'info') return { code: 0, output: JSON.stringify(changes.daemon ?? runtime.daemonId) }
    if (action === 'ls') return { code: 0, output: JSON.stringify({ ID: changes.badID ? 'unsafe' : (kind === 'container' ? 'd' : 'e').repeat(64) }) }
    if (kind === 'image') return { code: 0, output: JSON.stringify([{ Id: imageId, Config: { Labels: { 'io.twine-realm.build.installation': changes.foreignImage ? runtime.installationId : build.installationId } } }]) }
    if (action === 'inspect') {
      const labels = { ...baseLabels, 'io.twine-realm.deploy.role': kind === 'container' ? 'application' : 'ingress', ...(changes.foreign ? { 'io.twine-realm.deploy.installation': build.installationId } : {}) }
      const value = kind === 'container' ? { Id: id, Name: '/' + prefix + (changes.renamed ? '-foreign' : '-service'), Image: imageId, Config: { Image: imageId, Labels: labels } } : { Id: id, Name: prefix + '-http', Driver: 'bridge', Scope: 'local', Internal: false, EnableIPv4: true, EnableIPv6: false, Labels: labels, Options: { 'com.docker.network.bridge.host_binding_ipv4': '127.0.0.1' }, Containers: changes.busyNetwork ? { unknown: {} } : {} }
      return { code: 0, output: changes.nonunique ? '[]' : JSON.stringify([value]) }
    }
    if (action === 'rm') return { code: changes.removeFailed ? 1 : 0, output: '' }
    throw new Error('未声明测试协议')
  }
  return { calls, execute }
}

test('没有双安装证据时不接触Docker，实际清理固定原context且不继承秘密', async () => {
  const fake = fixture()
  await cleanupDeploymentResources({ execute: fake.execute })
  assert.equal(fake.calls.length, 0)
  await cleanupDeploymentResources({ runtimeInstallation: runtime, buildInstallation: build, execute: fake.execute, env: { PATH: '/tools', HOME: '/test', SONAR_TOKEN: 'synthetic-private-value' } })
  assert.equal(fake.calls.filter(call => call.args[3] === 'rm').length, 2)
  assert.ok(fake.calls.every(call => call.args[0] === '--context' && call.args[1] === runtime.context && call.settings.env.SONAR_TOKEN === undefined))
  const kinds = fake.calls.filter(call => call.args[3] === 'rm').map(call => call.args[2])
  assert.deepEqual(kinds, ['container', 'network'])
})

for (const changes of [{ failed: true }, { warning: true }, { invalid: true }, { daemon: 'foreign' }, { badID: true }, { foreign: true }, { foreignImage: true }, { renamed: true }, { nonunique: true }, { removeFailed: true }]) {
  test('未知业务资源保留：' + Object.keys(changes).join(','), async () => {
    const fake = fixture(changes)
    await assert.rejects(cleanupDeploymentResources({ runtimeInstallation: runtime, buildInstallation: build, execute: fake.execute, env: {} }), error => error.preserveTemporary && !error.message.includes('synthetic-private-fragment'))
    if (!changes.removeFailed) assert.equal(fake.calls.filter(call => call.args[3] === 'rm').length, 0)
  })
}

test('入口网络仍被使用时保留网络，其他安装或daemon不扩大清理范围', async () => {
  const fake = fixture({ busyNetwork: true })
  await assert.rejects(cleanupDeploymentResources({ runtimeInstallation: runtime, buildInstallation: build, execute: fake.execute }))
  assert.equal(fake.calls.some(call => call.args[2] === 'network' && call.args[3] === 'rm'), false)
  const other = fixture()
  await assert.rejects(cleanupDeploymentResources({ runtimeInstallation: runtime, buildInstallation: { ...build, environment: { ...build.environment, context: 'foreign' } }, execute: other.execute }))
  assert.equal(other.calls.length, 0)
})
