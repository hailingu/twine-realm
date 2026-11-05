/** 验证临时资源归属、报告传递与失败清理；真实 Docker 行为由完整门禁另行执行。 */
import assert from 'node:assert/strict'
import { access, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { runPlatformChecks } from './platform-check.mjs'

/** 模拟工具协议并在真实临时文件系统产生本次报告。 */
async function fixture(options, action) {
  const temporary = await mkdtemp(join(tmpdir(), 'twine-platform-protocol-'))
  const calls = []; const output = []; let configuration
  const execute = async (command, args, settings) => {
    calls.push({ command, args, settings })
    if (command.endsWith('/runtime-init')) return { code: 0, output: JSON.stringify({ schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000012', adapter: 'file-secrets-v1', context: 'local', daemonId: 'test-daemon' }) }
    if (command === 'docker' && args[0] === '--context' && args[2] === 'info') return { code: 0, output: JSON.stringify('test-daemon') }
    if (command === 'docker' && args[0] === 'run') {
      configuration = args[args.indexOf('--env-file') + 1]
      assert.equal((await stat(configuration)).mode & 0o777, 0o600)
      assert.match(await readFile(configuration, 'utf8'), /MYSQL_PASSWORD=[a-f0-9]{48}/)
      return { code: 0, output: 'test-owned-container\n' }
    }
    if (command === 'docker' && args.includes('--format={{.State.Health.Status}}')) return { code: 0, output: options.unhealthy ? 'unhealthy' : 'healthy' }
    if (command === 'docker' && args[0] === 'inspect') return { code: 0, output: '13307\n' }
    if (command === 'gofmt' && options.formatFailure) return { code: 0, output: 'server/unformatted.go\n' }
    if (command === 'go' && args[0] === 'env') return { code: 0, output: 'go1.27.3\n' }
    if (command === 'npm' && args.includes('lint') && options.lintFailure) return { code: 1, output: '类型规则拒绝\n' }
    if (command === 'go' && args[0] === 'test') {
      const flag = args.find(value => value.startsWith('-coverprofile='))
      const module = settings.cwd.endsWith('service-template') ? 'example.invalid/service/internal/app/handler.go' : 'github.com/hailingu/twine-realm/server/internal/project/service.go'
      await writeFile(flag.slice('-coverprofile='.length), `mode: atomic\n${module}:1.1,2.1 1 1\n`)
    }
    if (command === 'npm' && args.includes('--coverage.reportsDirectory')) {
      const report = args[args.indexOf('--coverage.reportsDirectory') + 1]
      await mkdir(report); await writeFile(join(report, 'lcov.info'), 'SF:src/project/model.ts\nDA:1,1\n')
    }
    if (command === 'go' && args[0] === 'tool') {
      const flag = args.find(value => value.startsWith('-o='))
      const module = settings.cwd.endsWith('service-template') ? 'example.invalid/service/cmd/service/main.go' : 'github.com/hailingu/twine-realm/server/cmd/platform/main.go'
      await writeFile(flag.slice(3), `mode: set\n${module}:1.1,2.1 1 1\n`)
    }
    return { code: 0, output: '' }
  }
  try { await action({ temporary, calls, output, execute, envFile: () => configuration }) }
  finally { await rm(temporary, { recursive: true, force: true }) }
}

test('完整检查使用独立数据库与临时构建，传递新报告并清理秘密和自有容器', async () => {
  await fixture({}, async fake => {
    const result = await runPlatformChecks({ root: process.cwd(), temporary: fake.temporary, env: { NO_COLOR: '1', FORCE_COLOR: '1' }, execute: fake.execute, write: value => fake.output.push(value) })
    assert.match(await readFile(result.goCoverageReportPaths[0], 'utf8'), /server\/internal\/project/)
    assert.match(await readFile(result.goCoverageReportPaths[0], 'utf8'), /server\/internal\/engineering\/assets\/go-net-http-v1\/_template\/internal\/app/)
    assert.match(await readFile(result.lcovReportPaths[0], 'utf8'), /SF:web\/src/)
    await assert.rejects(access(fake.envFile()), { code: 'ENOENT' })
    const cleanup = fake.calls.filter(call => call.command === 'docker' && call.args[0] === 'rm')
    assert.equal(cleanup.length, 1)
    assert.match(cleanup[0].args[2], /^twine-check-/)
    const browser = fake.calls.find(call => call.args.includes('test:e2e'))
    assert.ok(browser.settings.env.TWINE_TEST_WEB_DIRECTORY.startsWith(fake.temporary))
    assert.equal(browser.settings.env.FORCE_COLOR, '0')
    assert.equal(browser.settings.env.NO_COLOR, undefined)
    assert.doesNotMatch(fake.output.join(''), /twine:[a-f0-9]{48}/)
  })
})

for (const options of [{ unhealthy: true }, { formatFailure: true }, { lintFailure: true }]) {
  test(`检查失败保留失败结论并回收自有资源 ${JSON.stringify(options)}`, async () => {
    await fixture(options, async fake => {
      await assert.rejects(runPlatformChecks({ root: process.cwd(), temporary: fake.temporary, env: {}, execute: fake.execute, write: value => fake.output.push(value) }))
      assert.ok(fake.calls.some(call => call.command === 'docker' && call.args[0] === 'rm'))
      await assert.rejects(access(fake.envFile()), { code: 'ENOENT' })
      assert.equal(fake.calls.filter(call => call.args.includes('test:e2e')).length, 0)
    })
  })
}
