/** 验证临时资源归属、报告传递与失败清理；真实 Docker 行为由完整门禁另行执行。 */
import assert from 'node:assert/strict'
import { access, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { runPlatformChecks, mergeAtomicCoverage } from './platform-check.mjs'

/** 模拟工具协议并在真实临时文件系统产生本次报告。 */
async function fixture(options, action) {
  const temporary = await mkdtemp(join(tmpdir(), 'twine-platform-protocol-'))
  const calls = []; const output = []; let configuration
  const execute = async (command, args, settings) => {
    calls.push({ command, args, settings })
    if (command.endsWith('/runtime-init')) return { code: 0, output: JSON.stringify({ schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000012', adapter: 'file-secrets-v1', context: 'local', daemonId: 'test-daemon' }) }
    if (command.endsWith('/build-init')) return { code: 0, output: JSON.stringify({ schemaVersion: 1, installationId: '10000000-0000-4000-8000-000000000014', adapter: 'local-go-container-v1', environment: { context: 'local', daemonId: 'test-daemon', imageId: 'sha256:'+'a'.repeat(64), architecture: 'amd64' } }) }
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
    if (command === 'go' && args[0] === 'test' && options.goGroupFailure && args.includes(options.goGroupFailure)) return { code: 1, output: '真实测试分组失败\n' }
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
    const coverage = await readFile(result.goCoverageReportPaths[0], 'utf8')
    assert.match(coverage, /server\/internal\/project\/service.go:1.1,2.1 1 2/)
    const groups = fake.calls.filter(call => call.command === 'go' && call.args[0] === 'test' && !call.settings.cwd.endsWith('service-template'))
    assert.equal(groups.length, 2)
    assert.ok(groups[0].args.includes('-skip=^TestRealProjectRun'))
    assert.equal(groups[0].args.at(-1), './...')
    assert.ok(groups[1].args.includes('-run=^TestRealProjectRun'))
    assert.equal(groups[1].args.at(-1), './internal/httpapi')
    for (const group of groups) for (const flag of ['-count=1', '-p=1', '-race', '-tags=integration', '-coverpkg=./...']) assert.ok(group.args.includes(flag))
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


test('atomic分组覆盖率累加且拒绝模式坐标和语句数冲突，不破坏已有报告', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'twine-atomic-'))
  const first = join(directory, 'first.out'); const second = join(directory, 'second.out'); const target = join(directory, 'merged.out')
  try {
    await writeFile(first, 'mode: atomic\na.go:1.1,2.1 2 3\n')
    await writeFile(second, 'mode: atomic\na.go:1.1,2.1 2 7\nb.go:1.1,1.2 1 1\n')
    await mergeAtomicCoverage([first, second], target)
    const valid = await readFile(target, 'utf8')
    assert.match(valid, /a.go:1.1,2.1 2 10/); assert.match(valid, /b.go:1.1,1.2 1 1/)
    for (const invalid of ['mode: set\na.go:1.1,2.1 2 1', 'mode: atomic\na.go:1.1,2.1 3 1', 'mode: atomic\na.go:1.1,3.1 2 1', 'mode: atomic\na.go:2.1,1.1 2 1', 'mode: atomic\na.go:0.1,2.1 2 1', 'mode: atomic\nbroken', 'mode: atomic\na.go:1.1,2.1 2 9007199254740991']) {
      await writeFile(second, invalid+'\n')
      await assert.rejects(mergeAtomicCoverage([first, second], target))
      assert.equal(await readFile(target, 'utf8'), valid)
    }
  } finally { await rm(directory, { recursive: true, force: true }) }
})

for (const selector of ['-skip=^TestRealProjectRun', '-run=^TestRealProjectRun']) {
  test(`任一Go分组失败均拒绝成功报告并清理：${selector}`, async () => {
    await fixture({ goGroupFailure: selector }, async fake => {
      await assert.rejects(runPlatformChecks({ root: process.cwd(), temporary: fake.temporary, env: {}, execute: fake.execute, write: () => {} }))
      assert.ok(fake.calls.some(call => call.command === 'docker' && call.args[0] === 'rm'))
      if (selector.startsWith('-skip')) assert.ok(!fake.calls.some(call => call.args.includes('-run=^TestRealProjectRun')))
    })
  })
}

test('真实Go空函数与空分支生成同坐标零语句atomic块，必须保留并合并', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'twine-go-empty-block-'))
  const source = join(directory, 'source.out'); const target = join(directory, 'merged.out')
  try {
    // Go1.27.3 go test -race -coverprofile：func Empty() {} 与 if v {} 的实际输出。
    const profile = 'mode: atomic\ncoverproof/main.go:2.15,2.15 0 1\ncoverproof/main.go:3.23,3.28 1 1\ncoverproof/main.go:3.29,3.29 0 1\n'
    await writeFile(source, profile)
    await mergeAtomicCoverage([source, source], target)
    assert.equal(await readFile(target, 'utf8'), profile.replaceAll(' 1\n', ' 2\n'))
  } finally { await rm(directory, { recursive: true, force: true }) }
})
