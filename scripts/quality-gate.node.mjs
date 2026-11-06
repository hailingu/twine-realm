/** 验证完整门禁的真实编排契约、报告隔离和失败后的资源清理。 */
import assert from 'node:assert/strict'
import { access, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { runQualityGate } from './quality-gate.mjs'

/** 在外部工具边界记录调用并产生协议规定的本次报告。 */
function harness({ failTests = false, failIssues = false } = {}) {
  const calls = { commands: [], scans: [], output: [] }
  const write = message => calls.output.push(message)
  const execute = async (command, args, configuration) => {
    calls.commands.push({ command, args, configuration })
    const nativeReport = args.find(argument => argument.startsWith('--test-reporter-destination=') && argument !== '--test-reporter-destination=stdout')
    if (nativeReport) {
      const path = nativeReport.slice('--test-reporter-destination='.length)
      calls.temporary = dirname(path)
      if (failTests) return { code: 1, output: '本次脚本测试失败' }
      await writeFile(path, 'TN:\nSF:scripts/sonar-gate.mjs\nDA:1,1\nend_of_record\n')
    }
    if (command === process.execPath && args.includes('scripts/issue-status.mjs')) {
      if (failIssues) return { code: 1, output: '本次 issue 元数据校验失败' }
    }
    return { code: 0, output: '' }
  }
  const scan = async configuration => {
    calls.scans.push(configuration)
    for (const report of configuration.lcovReportPaths) await access(report)
    return { output: '', status: 'OK' }
  }
  const platform = async ({ temporary, env }) => { calls.platformEnv = env; const report = join(temporary, 'web-lcov.info'); const go = join(temporary, 'go.out'); await writeFile(report, 'TN:\nSF:web/src/project/model.ts\nDA:1,1\nend_of_record\n'); await writeFile(go, 'mode: atomic\n'); return { lcovReportPaths: [report], goCoverageReportPaths: [go] } }
  return { calls, execute, scan, write, platform }
}

const env = { SONAR_HOST_URL: 'http://sonar.invalid', SONAR_TOKEN: 'synthetic-quality-token' }

test('门禁执行一次脚本测试与 issue 校验并传递本次脚本覆盖率，结束后清理目录', async context => {
  const fake = harness()
  const stdout = context.mock.method(process.stdout, 'write', () => true)
  try {
    await runQualityGate({ env, execute: fake.execute, scan: fake.scan, write: fake.write, platform: fake.platform })
  } finally { stdout.mock.restore() }
  // 原生测试 runner 也写入 stdout；只约束门禁自身的正式诊断标记。
  const emitted = stdout.mock.calls.map(call => String(call.arguments[0])).join('')
  assert.doesNotMatch(emitted, /\[QUALITY_(?:STEP|GATE_OK)\]/)
  const commands = fake.calls.commands
  assert.equal(commands.length, 4)
  const [native] = commands
  assert.equal(native.command, process.execPath)
  assert.ok(native.args.includes('--experimental-test-coverage'))
  for (const file of ['sonar-gate', 'quality-gate', 'issue-status', 'deployment-check']) {
    assert.ok(native.args.includes(`scripts/${file}.node.mjs`))
  }
  const [issueCheck] = commands.slice(1)
  assert.deepEqual(issueCheck.args, ['scripts/issue-status.mjs'])
  assert.equal(fake.calls.scans.length, 1)
  const [scan] = fake.calls.scans
  assert.equal(scan.lcovReportPaths.length, 2)
  assert.equal(commands.filter(call => call.args.includes('audit')).length, 2)
  assert.ok(scan.lcovReportPaths[0].startsWith(fake.calls.temporary))
  assert.match(fake.calls.output.join(''), /QUALITY_STEP/)
  assert.match(fake.calls.output.join(''), /QUALITY_GATE_OK/)
  await assert.rejects(access(fake.calls.temporary), { code: 'ENOENT' })
})

test('脚本测试失败会停止 issue 校验与扫描且清理本次已有报告', async () => {
  const fake = harness({ failTests: true })
  await assert.rejects(runQualityGate({ env, execute: fake.execute, scan: fake.scan, write: fake.write, platform: fake.platform }), /QUALITY_CHECK_FAILED/)
  assert.equal(fake.calls.commands.length, 1, '测试失败后不得继续执行 issue 校验')
  assert.equal(fake.calls.scans.length, 0)
  assert.match(fake.calls.output.join(''), /本次脚本测试失败/)
  assert.doesNotMatch(fake.calls.output.join(''), /QUALITY_GATE_OK/)
  await assert.rejects(access(fake.calls.temporary), { code: 'ENOENT' })
})

test('子工具、平台夹具与扫描不继承 hook 的 Git 定位，调用者环境保持不变', async () => {
  const fake = harness()
  const caller = { ...env, PATH: '/controlled/bin', GIT_DIR: '/caller/git', GIT_INDEX_FILE: '/caller/index', GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'core.bare', GIT_CONFIG_VALUE_0: 'true' }
  const before = { ...caller }
  await runQualityGate({ env: caller, execute: fake.execute, scan: fake.scan, write: fake.write, platform: fake.platform })
  for (const child of [...fake.calls.commands.map(call => call.configuration.env), fake.calls.platformEnv, fake.calls.scans[0].env]) {
    assert.deepEqual(child, { ...env, PATH: '/controlled/bin' })
  }
  assert.deepEqual(caller, before)
})

test('issue 校验失败会停止扫描且不宣告整体通过', async () => {
  const fake = harness({ failIssues: true })
  await assert.rejects(runQualityGate({ env, execute: fake.execute, scan: fake.scan, write: fake.write, platform: fake.platform }), /QUALITY_CHECK_FAILED/)
  assert.equal(fake.calls.commands.length, 2, 'issue 校验是测试之后的第二步')
  assert.equal(fake.calls.scans.length, 0)
  assert.match(fake.calls.output.join(''), /本次 issue 元数据校验失败/)
  assert.doesNotMatch(fake.calls.output.join(''), /QUALITY_GATE_OK/)
  await assert.rejects(access(fake.calls.temporary), { code: 'ENOENT' })
})

test('运行清理未知时上层门禁保留绑定文件，不以普通失败清理覆盖资源所有者决定', async () => {
  const fake = harness()
  let retained
  const platform = async ({ temporary }) => {
    retained = temporary
    await writeFile(join(temporary, 'owned-bind-marker'), '测试自有文件，无秘密')
    const failure = new Error('受控清理结果未知')
    failure.preserveTemporary = true
    throw new AggregateError([failure], '运行资源未确认清理')
  }
  try {
    await assert.rejects(runQualityGate({ env, execute: fake.execute, scan: fake.scan, write: fake.write, platform }), /运行资源未确认清理/)
    assert.equal(fake.calls.scans.length, 0)
    await access(join(retained, 'owned-bind-marker'))
    assert.match(fake.calls.output.join(''), /运行测试现场保留在/)
    assert.doesNotMatch(fake.calls.output.join(''), /QUALITY_GATE_OK/)
  } finally { if (retained) await rm(retained, { recursive: true, force: true }) }
})
