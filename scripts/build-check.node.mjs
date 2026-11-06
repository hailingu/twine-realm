/** 构建清理协议证明只处理本次完整归属；未知沙箱、卷、daemon和镜像都保留。 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { cleanupBuildResources, parseBuildInstallation } from './build-check.mjs'

const hash = 'sha256:' + 'a'.repeat(64)
const installation = { schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000014', adapter: 'local-go-container-v1', environment: { context: 'test-local', daemonId: 'test-daemon', imageId: hash, architecture: 'amd64' } }
const owned = { Id: hash, Os: 'linux', Architecture: 'amd64', Config: { User: '65532:65532', Labels: { 'io.twine-realm.build.installation': installation.installationId, 'io.twine-realm.build.operation': '20000000-0000-4000-8000-000000000014', 'io.twine-realm.build.target': '30000000-0000-4000-8000-000000000014', 'io.twine-realm.build.input': hash } }, RepoTags: ['tr-build-' + 'a'.repeat(32) + '-candidate:local', 'keep-another-tag:local'] }

/** @param changes 边界故障 @returns 受控工具输出与真实调用记录。 */
function fixture(changes = {}) {
  const calls = []
  const execute = async (_command, args, options) => {
    calls.push({ args, options })
    let value = ''
    if (args[2] === 'info') value = JSON.stringify(changes.daemon ?? 'test-daemon')
    if (args[2] === 'container' && changes.container) value = JSON.stringify({ ID: 'still-running' })
    if (args[2] === 'volume' && changes.volume) value = JSON.stringify({ Name: 'still-present' })
    if (args[2] === 'image' && args[3] === 'ls') value = JSON.stringify({ ID: hash })
    if (args[2] === 'image' && args[3] === 'inspect') value = JSON.stringify([changes.image ?? owned])
    return { code: changes.failed ? 1 : 0, output: changes.invalidJSON ? 'synthetic-private-fragment' : value }
  }
  return { calls, execute }
}

test('初始化和清理不回显坏JSON，未初始化时不访问Docker', async () => {
  assert.throws(() => parseBuildInstallation('synthetic-private-fragment'), error => !error.message.includes('synthetic-private-fragment'))
  assert.throws(() => parseBuildInstallation('{}'))
  assert.equal(parseBuildInstallation(JSON.stringify(installation)).environment.context, 'test-local')
  const fake = fixture(); await cleanupBuildResources({ execute: fake.execute }); assert.equal(fake.calls.length, 0)
})

test('只删除本次镜像tag，环境不传秘密且其他tag保持', async () => {
  const fake = fixture()
  await cleanupBuildResources({ installation, execute: fake.execute, env: { PATH: '/test/bin', HOME: '/test/home', TWINE_DATABASE_DSN: 'synthetic-private-value' } })
  const removed = fake.calls.filter(call => call.args[3] === 'rm')
  assert.equal(removed.length, 1)
  assert.equal(removed[0].args[4], owned.RepoTags[0])
  assert.ok(fake.calls.every(call => !JSON.stringify(call).includes('synthetic-private-value')))
})

for (const changes of [{ daemon: 'different' }, { container: true }, { volume: true }, { failed: true }, { invalidJSON: true }, { image: { ...owned, Id: 'sha256:'+'b'.repeat(64) } }, { image: { ...owned, Config: { ...owned.Config, User: '0' } } }, { image: { ...owned, RepoTags: ['foreign:local'] } }]) {
  test(`未知清理保留现场：${Object.keys(changes).join(',')}`, async () => {
    const fake = fixture(changes)
    await assert.rejects(cleanupBuildResources({ installation, execute: fake.execute }), error => error.preserveTemporary === true && !error.message.includes('synthetic-private-fragment'))
    assert.ok(!fake.calls.some(call => call.args[3] === 'rm'))
  })
}
