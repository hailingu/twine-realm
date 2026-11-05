/** 验证 Sonar 门禁的配置、真实扫描任务归属和失败传播契约。 */
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { loadSonarSettings, redactOutput, runCommand, runSonarGate } from './sonar-gate.mjs'

/** 构造测试自有环境，使用合成凭据而非用户配置。 */
function environment(overrides = {}) {
  return { SONAR_HOST_URL: 'http://127.0.0.1:9000', TWINE_REALM_SONAR_TOKEN: 'synthetic-test-token', ...overrides }
}

test('缺少凭据、非法地址和超时配置会在启动扫描前拒绝', () => {
  assert.throws(() => loadSonarSettings({}), /SONAR_CONFIG/)
  assert.throws(() => loadSonarSettings(environment({ SONAR_HOST_URL: 'file:///tmp/sonar' })), /SONAR_CONFIG/)
  assert.throws(() => loadSonarSettings(environment({ SONAR_HOST_URL: 'http://user:pass@localhost' })), /SONAR_CONFIG/)
  assert.throws(() => loadSonarSettings(environment({ SONAR_GATE_TIMEOUT_SECONDS: '0' })), /SONAR_CONFIG/)
})

test('环境凭据优先级明确且输出隐藏凭据', () => {
  assert.equal(loadSonarSettings(environment({ SONAR_TOKEN: 'preferred-secret' })).token, 'preferred-secret')
  assert.equal(redactOutput('a synthetic-test-token b', 'synthetic-test-token'), 'a [已隐藏] b')
})

/** 构造带真实报告文件的扫描装置，HTTP 替身只返回固定协议数据。 */
async function withFixture(callback) {
  const root = await mkdtemp(join(tmpdir(), 'twine-sonar-test-'))
  await writeFile(join(root, 'sonar-project.properties'), 'sonar.projectKey=twine-realm\n')
  try { await callback(root) } finally { await rm(root, { recursive: true, force: true }) }
}

/** 构造可记录请求的扫描器和服务端应答。 */
function harness(options = {}) {
  const calls = { commands: [], requests: [] }
  return {
    calls,
    runCommand: async (command, args, configuration) => {
      calls.commands.push({ command, args, env: configuration.env, timeoutMs: configuration.timeoutMs })
      const report = args.find(value => value.startsWith('-Dsonar.scanner.metadataFilePath='))?.split('=').slice(1).join('=')
      if (report && !options.noReport) await writeFile(report, 'projectKey=twine-realm\nceTaskId=task-owned\n')
      return { code: options.scannerExit ?? 0, output: 'scanner synthetic-test-token' }
    },
    fetchImpl: async (url, configuration) => {
      const address = new URL(url)
      calls.requests.push({ address, configuration })
      if (options.httpError) return { ok: false, status: 503 }
      if (address.pathname.endsWith('/issues/search')) return { ok: true, json: async () => ({ total: options.openIssues ?? 0 }) }
      const data = address.pathname.endsWith('/ce/task')
        ? { task: { status: options.taskStatus ?? 'SUCCESS', analysisId: 'analysis-owned', componentKey: 'twine-realm' } }
        : { projectStatus: { status: options.gateStatus ?? 'OK', conditions: [] } }
      return { ok: true, json: async () => data }
    }
  }
}

/** 等待内核回收已终止的后代，避免把瞬时进程回收延迟误判为存活。 */
async function processHasExited(pid) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    try { process.kill(pid, 0) } catch (error) {
      if (error.code === 'ESRCH') return true
      throw error
    }
    await delay(10)
  }
  return false
}

for (const parentExitsEarly of [false, true]) {
  test(`短超时会终止继承输出管道的后代（父进程提前退出：${parentExitsEarly}）`, {
    skip: process.platform === 'win32'
  }, async () => {
    const source = `
      const { spawn } = require('node:child_process')
      const child = spawn(process.execPath, ['-e', 'setTimeout(() => {}, 3000)'], { stdio: 'inherit' })
      process.stdout.write(String(child.pid) + '\\n')
      ${parentExitsEarly ? 'child.unref(); process.exit(0)' : "child.once('exit', () => process.exit(0))"}
    `
    const started = Date.now()
    const result = await runCommand(process.execPath, ['-e', source], { timeoutMs: 600 })
    assert.notEqual(result.code, 0)
    assert.ok(Date.now() - started < 2000, '不能等后代的自然退出才返回')
    const descendantPid = Number(result.output.trim().split('\n')[0])
    assert.ok(Number.isInteger(descendantPid), '后代标识必须为整数')
    assert.ok(descendantPid > 0, '必须实际启动后代')
    assert.ok(await processHasExited(descendantPid), '超时返回后后代不得继续运行')
  })
}

test('正常命令保留输出与退出结果，启动失败返回非零', async () => {
  const success = await runCommand(process.execPath, ['-e', "process.stdout.write('完成')"], { timeoutMs: 3000 })
  assert.deepEqual(success, { code: 0, output: '完成' })
  const failure = await runCommand('twine-test-command-does-not-exist', [], { timeoutMs: 3000 })
  assert.notEqual(failure.code, 0)
})

test('Sonar 新代码门槛通过但仍有历史代码问题时拒绝提交', async () => {
  await withFixture(async root => {
    const fake = harness({ openIssues: 1 })
    await assert.rejects(runSonarGate({ root, env: environment(), ...fake }), /SONAR_OPEN_ISSUES/)
  })
})

test('非法命令超时配置在启动子进程前拒绝', async () => {
  for (const timeoutMs of [0, -1, NaN, Infinity, 1.5, 2147483648]) {
    await assert.rejects(runCommand(process.execPath, ['-e', "process.stdout.write('不应启动')"], {
      timeoutMs
    }), /QUALITY_COMMAND_CONFIG/)
  }
})

test('外层进程中断时终止正在运行的独立进程组', { skip: process.platform === 'win32' }, async () => {
  const source = `
    import { runCommand } from ${JSON.stringify(new URL('./sonar-gate.mjs', import.meta.url).href)}
    const pending = runCommand(process.execPath, ['-e', 'setTimeout(() => {}, 700)'], { timeoutMs: 3000 })
    process.stdout.write('ready\\n')
    const result = await pending
    process.stdout.write(JSON.stringify(result))
    process.exitCode = result.code
  `
  const command = spawn(process.execPath, ['--input-type=module', '-e', source], { stdio: ['ignore', 'pipe', 'pipe'] })
  const chunks = []
  command.stdout.on('data', chunk => chunks.push(chunk))
  const completed = once(command, 'close')
  const watchdog = setTimeout(() => command.kill('SIGKILL'), 4000)
  try {
    await Promise.race([once(command.stdout, 'data'), completed.then(() => { throw new Error('测试进程未准备就绪') })])
    command.kill('SIGTERM')
    const [code, signal] = await completed
    assert.equal(signal, null)
    assert.equal(code, 1)
    assert.match(Buffer.concat(chunks).toString('utf8'), /QUALITY_COMMAND_CANCELLED/)
  } finally {
    clearTimeout(watchdog)
    command.kill('SIGKILL')
    await delay(750)
  }
})

test('扫描预算覆盖完整的最大门禁等待时间与扫描工作', async () => {
  await withFixture(async root => {
    const fake = harness()
    await runSonarGate({ root, env: environment({ SONAR_GATE_TIMEOUT_SECONDS: '3600' }), ...fake })
    assert.ok(fake.calls.commands[0].timeoutMs > 3600000, '最大门禁等待不能被较短的命令超时截断')
    assert.ok(fake.calls.commands[0].args.includes('-Dsonar.qualitygate.timeout=3600'))
  })
})

test('平台尚无 Java 源码：不提供目录时不传 Java 参数，提供缺失目录时拒绝启动', async () => {
  await withFixture(async root => {
    const fake = harness()
    await runSonarGate({ root, env: environment(), ...fake })
    assert.equal(fake.calls.commands[0].args.some(value => value.startsWith('-Dsonar.java.')), false,
      '未提供 Java 构建目录时不得猜测或回退任何 Java 字节码参数')
    const javaBuildDirectory = join(root, 'java-build')
    const missing = harness()
    await assert.rejects(
      runSonarGate({ root, javaBuildDirectories: [javaBuildDirectory], env: environment(), ...missing }),
      /SONAR_JAVA_MISSING/
    )
    assert.equal(missing.calls.commands.length, 0, '字节码缺失时不得启动扫描')
    // 补齐本次构建产物后才正式传给扫描器，不允许静默降级。
    await mkdir(join(javaBuildDirectory, 'classes'), { recursive: true })
    await mkdir(join(javaBuildDirectory, 'test-classes'))
    const present = harness()
    await runSonarGate({ root, javaBuildDirectories: [javaBuildDirectory], env: environment(), ...present })
    assert.ok(present.calls.commands[0].args.includes(`-Dsonar.java.binaries=${join(javaBuildDirectory, 'classes')}`))
    assert.ok(present.calls.commands[0].args.includes(`-Dsonar.java.test.binaries=${join(javaBuildDirectory, 'test-classes')}`))
  })
})

test('覆盖率报告必须来自本次指定文件并通过扫描器正式参数传递', async () => {
  await withFixture(async root => {
    const lcovReportPath = join(root, 'coverage/lcov.info')
    await mkdir(join(root, 'coverage'))
    // 使用 LCOV 的正式 SF/DA 记录验证报告传递，不把合成样本作为产品覆盖率证据。
    await writeFile(lcovReportPath, 'TN:\nSF:scripts/sonar-gate.mjs\nDA:1,1\nLF:1\nLH:1\nend_of_record\n')
    const lcovReportPaths = [lcovReportPath]
    const fake = harness()
    await runSonarGate({ root, lcovReportPaths, env: environment(), ...fake })
    assert.ok(fake.calls.commands[0].args.includes(`-Dsonar.javascript.lcov.reportPaths=${lcovReportPaths.join(',')}`))
    await rm(lcovReportPath)
    const missing = harness()
    await assert.rejects(runSonarGate({ root, lcovReportPaths, env: environment(), ...missing }), /SONAR_COVERAGE_MISSING/)
    assert.equal(missing.calls.commands.length, 0)
  })
})

test('Java 覆盖率必须来自本次构建的 JaCoCo 报告并正式传递', async () => {
  await withFixture(async root => {
    const jacocoReportPath = join(root, 'java-build/site/jacoco/jacoco.xml')
    await mkdir(join(root, 'java-build/site/jacoco'), { recursive: true })
    await writeFile(jacocoReportPath, '<?xml version="1.0"?><report/>')
    const fake = harness()
    await runSonarGate({ root, javaCoverageReportPaths: [jacocoReportPath], env: environment(), ...fake })
    assert.ok(fake.calls.commands[0].args.includes(`-Dsonar.coverage.jacoco.xmlReportPaths=${jacocoReportPath}`))
    await rm(jacocoReportPath)
    const missing = harness()
    await assert.rejects(runSonarGate({ root, javaCoverageReportPaths: [jacocoReportPath], env: environment(), ...missing }), /SONAR_COVERAGE_MISSING/)
    assert.equal(missing.calls.commands.length, 0, '覆盖率缺失时不得启动扫描')
  })
})

test('Go 覆盖率只采用本次存在的报告并以正式参数传递', async () => {
  await withFixture(async root => {
    const report = join(root, 'go.out')
    await writeFile(report, 'mode: atomic\n')
    const fake = harness()
    await runSonarGate({ root, goCoverageReportPaths: [report], env: environment(), ...fake })
    assert.ok(fake.calls.commands[0].args.includes(`-Dsonar.go.coverage.reportPaths=${report}`))
    await rm(report)
    await assert.rejects(runSonarGate({ root, goCoverageReportPaths: [report], env: environment(), ...harness() }), /SONAR_COVERAGE_MISSING/)
  })
})

test('扫描失败不查询服务端，诊断不包含凭据', async () => {
  await withFixture(async root => {
    const fake = harness({ scannerExit: 1 })
    await assert.rejects(runSonarGate({ root, env: environment(), ...fake }), error => {
      assert.match(error.message, /SONAR_SCAN_FAILED/)
      assert.doesNotMatch(error.message, /synthetic-test-token/)
      return true
    })
    assert.equal(fake.calls.requests.length, 0)
  })
})

test('只接受本次任务的成功分析与 OK 门禁，凭据不进入命令行', async () => {
  await withFixture(async root => {
    const fake = harness()
    const result = await runSonarGate({ root, env: environment(), ...fake })
    assert.equal(result.analysisId, 'analysis-owned')
    assert.equal(result.status, 'OK')
    assert.equal(fake.calls.commands[0].env.SONAR_TOKEN, 'synthetic-test-token')
    assert.equal(fake.calls.commands[0].args.some(value => value.includes('synthetic-test-token')), false)
    assert.equal(fake.calls.commands[0].args.includes('-Dsonar.qualitygate.wait=true'), true)
    assert.equal(fake.calls.requests[0].address.searchParams.get('id'), 'task-owned')
    assert.equal(fake.calls.requests[1].address.searchParams.get('analysisId'), 'analysis-owned')
  })
})

for (const gateStatus of ['ERROR', 'WARN', 'NONE', undefined]) {
  test(`服务端门禁 ${String(gateStatus)} 不能被当作通过`, async () => {
    await withFixture(async root => {
      const fake = harness({ gateStatus: gateStatus ?? 'UNRECOGNIZED' })
      await assert.rejects(runSonarGate({ root, env: environment(), ...fake }), /SONAR_GATE_REJECTED/)
    })
  })
}

test('无报告、任务未成功和服务端读取错误均阻止门禁', async () => {
  for (const options of [{ noReport: true }, { taskStatus: 'FAILED' }, { httpError: true }]) {
    await withFixture(async root => {
      await assert.rejects(runSonarGate({ root, env: environment(), ...harness(options) }), /SONAR_/)
    })
  }
})
