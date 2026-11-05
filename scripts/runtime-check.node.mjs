/** 验证一次性测试清理的所有权、daemon固定、警告失败、环境隔离与保留边界。 */
import assert from 'node:assert/strict'
import test from 'node:test'
import { cleanupRuntimeResources, parseRuntimeInstallation, requiresPreservation } from './runtime-check.mjs'

const installation = { schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000012', adapter: 'file-secrets-v1', context: 'local', daemonId: 'test-daemon' }
const labels = { 'io.twine-realm.installation': installation.installationId, 'io.twine-realm.adapter': 'mysql-compose-v1', 'io.twine-realm.resource': '20000000-0000-4000-8000-000000000012', 'io.twine-realm.project': '30000000-0000-4000-8000-000000000012', 'io.twine-realm.component': '40000000-0000-4000-8000-000000000012' }
const identities = { container: 'a'.repeat(64), network: 'b'.repeat(64), volume: 'tr-' + 'c'.repeat(32) + '-data' }

/** 只模拟工具协议；真实资源效果由平台完整检查验证。 */
function fixture(options = {}) {
  const calls = []
  const execute = async (command, args, settings) => {
    calls.push({ command, args, settings })
    const [prefix, context, kind, action, identity] = args
    assert.equal(prefix, '--context'); assert.equal(context, 'local')
    if (kind === 'info') return { code: 0, output: JSON.stringify(options.wrongDaemon ? 'other-daemon' : installation.daemonId) }
    if (options.warning) return { code: 0, output: 'level=warning controlled warning\n' }
    if (action === 'ls') return { code: 0, output: JSON.stringify(kind === 'volume' ? { Name: identities[kind] } : { ID: identities[kind] }) + '\n' }
    if (action === 'inspect') {
      const ownership = { ...labels, ...(options.foreign ? { 'io.twine-realm.installation': 'other-installation' } : {}) }
      const value = kind === 'container' ? { Id: identity, Config: { Labels: ownership } } : kind === 'volume' ? { Name: identity, Driver: options.unsafeVolume ? 'foreign' : 'local', Options: {}, Labels: ownership } : { Id: identity, Labels: ownership }
      return { code: 0, output: options.malformed ? 'synthetic-private-output' : JSON.stringify([value]) }
    }
    if (action === 'rm') return { code: options.removeFailure ? 1 : 0, output: '' }
    throw new Error('未声明测试命令')
  }
  return { execute, calls }
}

test('初始化清理授权只接受精确非敏感报告，不回显未知输出', () => {
  assert.deepEqual(parseRuntimeInstallation(JSON.stringify(installation)), installation)
  for (const value of [{ ...installation, extra: true }, { ...installation, context: '../remote' }, { ...installation, context: true }, { ...installation, daemonId: 1 }, { ...installation, installationId: 'not-an-id' }]) assert.throws(() => parseRuntimeInstallation(JSON.stringify(value)))
  assert.throws(() => parseRuntimeInstallation('synthetic-private-output'), error => !error.message.includes('synthetic-private-output'))
})

test('仅清理本次安装实际归属对象，固定context且不继承云或Sonar秘密', async () => {
  const fake = fixture()
  await cleanupRuntimeResources({ installation, execute: fake.execute, env: { PATH: '/tools', HOME: '/test', SONAR_TOKEN: 'synthetic', AWS_SECRET_ACCESS_KEY: 'synthetic' } })
  assert.equal(fake.calls.filter(call => call.args[3] === 'rm').length, 3)
  for (const call of fake.calls) {
    assert.equal(call.settings.env.SONAR_TOKEN, undefined)
    assert.equal(call.settings.env.AWS_SECRET_ACCESS_KEY, undefined)
  }
})

for (const options of [{ wrongDaemon: true }, { foreign: true }, { malformed: true }, { warning: true }]) {
  test(`daemon、归属或结果不确定时保持原对象和现场 ${JSON.stringify(options)}`, async () => {
    const fake = fixture(options)
    await assert.rejects(cleanupRuntimeResources({ installation, execute: fake.execute, env: {} }), error => requiresPreservation(error) && !error.message.includes('synthetic-private-output'))
    assert.equal(fake.calls.some(call => call.args[3] === 'rm'), false)
  })
}

test('部分清理失败保留明确失败与私有材料，不静默删除凭据目录', async () => {
  for (const options of [{ removeFailure: true }, { unsafeVolume: true }]) {
    const fake = fixture(options)
    await assert.rejects(cleanupRuntimeResources({ installation, execute: fake.execute, env: {} }), requiresPreservation)
  }
  assert.equal(requiresPreservation(new AggregateError([Object.assign(new Error('保留'), { preserveTemporary: true })])), true)
  await cleanupRuntimeResources({ installation: undefined })
})
