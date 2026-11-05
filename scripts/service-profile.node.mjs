/** 验证生成漂移、模板门禁失败传播、环境隔离与本次报告，不替代真实数据库验收。 */
import assert from 'node:assert/strict'
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFileSync } from 'node:child_process'
import test from 'node:test'
import { runServiceProfileChecks } from './service-profile-check.mjs'

test('离线发布核实实际文件，缺失 / 改变材料不能通过原版本摘要', async () => {
  const root = await mkdtemp(join(tmpdir(), 'twine-profile-release-'))
  try {
    await mkdir(join(root, 'scripts'))
    const source = new URL('../server/internal/engineering/assets/go-net-http-v1/', import.meta.url)
    await cp(source, join(root, 'server/internal/engineering/assets/go-net-http-v1'), { recursive: true })
    const script = join(root, 'scripts/service-profile.mjs')
    await cp(new URL('./service-profile.mjs', import.meta.url), script)
    const check = () => execFileSync(process.execPath, [script, '--check'], { encoding: 'utf8', stdio: 'pipe' })
    assert.match(check(), /SERVICE_PROFILE_OK/)
    const document = join(root, 'server/internal/engineering/assets/go-net-http-v1/_template/docs/engineering/general.md')
    await writeFile(document, '另一个版本的规范\n')
    assert.throws(check)
    execFileSync(process.execPath, [script], { stdio: 'pipe' })
    assert.match(check(), /SERVICE_PROFILE_OK/)
    await rm(document)
    assert.throws(check)
  } finally { await rm(root, { recursive: true, force: true }) }
})

/** 用真实临时报告验证工具协议，所有执行失败继续保持非零。 */
async function fixture(options, action) {
  const temporary = await mkdtemp(join(tmpdir(), 'twine-profile-protocol-'))
  const calls = []; const output = []
  const execute = async (command, args, settings) => {
    calls.push({ command, args, settings })
    if (command === 'go' && args[0] === 'env') return { code: 0, output: options.version ?? 'go1.27.3\n' }
    if (command === 'gofmt' && options.format) return { code: 0, output: 'internal/app/handler.go\n' }
    if (command === 'go' && args[0] === 'vet' && options.vet) return { code: 1, output: '静态检查失败\n' }
    if (command === 'go' && args[0] === 'test') await writeFile(args.find(value => value.startsWith('-coverprofile=')).slice(14), 'mode: atomic\nexample.invalid/service/internal/app/handler.go:1.1,2.1 1 1\n')
    if (command === 'go' && args[0] === 'tool') await writeFile(args.find(value => value.startsWith('-o=')).slice(3), 'mode: atomic\nexample.invalid/service/cmd/service/main.go:1.1,2.1 1 1\n')
    return { code: 0, output: '' }
  }
  try { await action({ temporary, calls, output, execute }) }
  finally { await rm(temporary, { recursive: true, force: true }) }
}

test('模板检查使用原版本和独立构建环境，仅受信集成测试得到管理配置', async () => {
  await fixture({}, async fake => {
    const result = await runServiceProfileChecks({ root: process.cwd(), temporary: fake.temporary, adminDSN: 'synthetic-admin-config', env: { PATH: '/tools', SONAR_TOKEN: 'synthetic-sonar', AWS_SECRET_ACCESS_KEY: 'synthetic-cloud', GIT_INDEX_FILE: '/outside' }, execute: fake.execute, write: value => fake.output.push(value) })
    assert.equal(result.goCoverageReportPaths.length, 2)
    for (const report of result.goCoverageReportPaths) assert.match(await readFile(report, 'utf8'), /example.invalid\/service/)
    const build = fake.calls.find(call => call.command === 'go' && call.args[0] === 'build' && call.settings.cwd.endsWith('service-template'))
    for (const key of ['SONAR_TOKEN', 'AWS_SECRET_ACCESS_KEY', 'GIT_INDEX_FILE', 'TWINE_TEMPLATE_ADMIN_DSN']) assert.equal(build.settings.env[key], undefined)
    const integration = fake.calls.find(call => call.command === 'go' && call.args[0] === 'test')
    assert.equal(integration.settings.env.TWINE_TEMPLATE_ADMIN_DSN, 'synthetic-admin-config')
    assert.equal(integration.settings.env.GOTOOLCHAIN, 'local')
    assert.doesNotMatch(fake.output.join(''), /synthetic-admin-config|synthetic-sonar|synthetic-cloud/)
  })
})

for (const options of [{ version: 'go1.28.0' }, { format: true }, { vet: true }]) {
  test(`模板真实检查未满足时不产生通过报告 ${JSON.stringify(options)}`, async () => {
    await fixture(options, async fake => {
      await assert.rejects(runServiceProfileChecks({ root: process.cwd(), temporary: fake.temporary, adminDSN: 'synthetic-admin-config', execute: fake.execute, write: value => fake.output.push(value) }))
      assert.equal(fake.calls.some(call => call.command === 'go' && call.args[0] === 'test'), false)
    })
  })
}

test('没有隔离管理配置不能把模板集成检查视为通过', async () => {
  await assert.rejects(runServiceProfileChecks({ root: process.cwd(), temporary: tmpdir(), adminDSN: '' }))
})
