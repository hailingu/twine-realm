/** 在独立 MySQL 和临时产物目录验证平台；任何失败都清理本任务拥有的容器。 */
import { randomBytes, randomUUID } from 'node:crypto'
import { access, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { runCommand } from './sonar-gate.mjs'
import { checkCommand } from './check-command.mjs'
import { cleanupRuntimeResources, parseRuntimeInstallation, requiresPreservation } from './runtime-check.mjs'
import { runServiceProfileChecks } from './service-profile-check.mjs'
import { parseBuildInstallation, cleanupBuildResources } from './build-check.mjs'
import { cleanupDeploymentResources } from './deployment-check.mjs'
import { createMySQLBudget } from './mysql-test-budget.mjs'
import { discoverPlatformTests, discoverBrowserTests, goGroups, isolatedCase, verifyGoCompletion } from './platform-isolation.mjs'

const MYSQL_IMAGE = 'mysql:9.7.4@sha256:df90258da46f6e7ff8574ff84f17618aba0765cac749928299c69fd4e7cb8ad8'
const BUILD_IMAGE = 'golang:1.27.3@sha256:0c050feb47802ae436b9030833694a0b80160d08e3b3049ad30bf7c9e724e09d'

/** 等待容器的真实健康信号，失败时不回显容器环境或数据库日志。 */
async function waitForDatabase(name, execute, env) {
  const deadline = Date.now() + 120000
  while (Date.now() < deadline) {
    const result = await execute('docker', ['inspect', '--format={{.State.Health.Status}}', name], { env })
    if (result.code !== 0 || result.output.trim() === 'unhealthy') throw new Error('临时 MySQL 健康检查失败')
    if (result.output.trim() === 'healthy') return
    await delay(750)
  }
  throw new Error('临时 MySQL 未在期限内就绪')
}

/** 合并浏览器进程和集成测试的真实覆盖率，同一代码块累加观测次数。 */
async function mergeGoCoverage(reports, target) {
  const merged = new Map()
  for (const report of reports) for (const line of (await readFile(report, 'utf8')).trim().split('\n').slice(1)) {
    const [location, statements, count] = line.split(' ')
    const previous = merged.get(location)
    merged.set(location, { statements, count: Number(count) + (previous?.count || 0) })
  }
  const lines = [...merged].map(([location, value]) => [location.replace('github.com/hailingu/twine-realm/server/', 'server/').replace('example.invalid/service/', 'server/internal/engineering/assets/go-net-http-v1/_template/'), value.statements, value.count].join(' '))
  await writeFile(target, 'mode: atomic\n' + lines.join('\n') + '\n')
}

/** 合并同一次运行的两个atomic测试分组；拒绝损坏或版本不一致的覆盖率块。 */
export async function mergeAtomicCoverage(reports, target) {
  const blocks = new Map()
  const coordinates = new Map()
  for (const report of reports) {
    const lines = (await readFile(report, 'utf8')).trim().split('\n')
    if (lines.shift() !== 'mode: atomic') throw new Error('测试分组覆盖率模式必须为atomic')
    for (const line of lines) {
      const { location, numbers } = parseAtomicBlock(line, coordinates)
      const previous = blocks.get(location)
      if (previous && previous.statements !== numbers[4]) throw new Error('测试分组覆盖率语句数不一致')
      const total = (previous?.count ?? 0) + numbers[5]
      if (!Number.isSafeInteger(total)) throw new Error('测试分组覆盖率计数溢出')
      blocks.set(location, { statements: numbers[4], count: total })
    }
  }
  if (!blocks.size) throw new Error('测试分组覆盖率为空')
  const lines = [...blocks].map(([location, value]) => `${location} ${value.statements} ${value.count}`)
  await writeFile(target, `mode: atomic\n${lines.join('\n')}\n`)
}

/** 清理错误与原检查错误一同报告，不让 finally 替换原失败。 */
async function cleanupDatabase(started, name, envFile, execute, env) {
  const failures = []
  if (started) {
    try {
      const result = await execute('docker', ['rm', '--force', name], { env })
      if (result.code !== 0) failures.push(new Error(`临时测试容器未清理：${name}`))
    } catch (error) { failures.push(error) }
  }
  try { await rm(envFile, { force: true }) } catch (error) { failures.push(error) }
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}

/** 执行真实模块检查并返回本次覆盖率路径；调用者拥有 temporary 的清理。 */
export async function runPlatformChecks({ root, temporary, env = process.env, execute = runCommand, write = message => process.stdout.write(message), budgetFactory = createDefaultBudget }) {
  const budget = await budgetFactory({ directory: join(temporary, 'docker-budget'), execute, env })
  const failures = []
  let reports
  try {
    reports = await runPlatformImplementation({ root, temporary, env: budget.env, execute: budget.execute, write, assertIdle: budget.assertIdle })
  } catch (error) { failures.push(error) }
  if (!failures.some(error => requiresPreservation(error))) {
    try { await budget.assertIdle(); await budget.release() }
    catch (error) { error.preserveTemporary = true; failures.push(error) }
  }
  if (failures.length) {
    await reportBudgetDiagnostics(budget, write, failures)
    throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
  }
  return reports
}

/** 只读预算模块白名单诊断，不能回显底层stderr或覆盖原始失败。 */
async function reportBudgetDiagnostics(budget, write, failures) {
  try {
    for (const line of await budget.readDiagnostics()) write(line + '\n')
  } catch {
    failures.push(new Error('本轮预算安全诊断读取或输出失败'))
  }
}

/** 只有默认真实预算需要发现宿主Docker；注入模拟预算不访问宿主工具。 */
async function createDefaultBudget(options) {
  const dockerPath = await findDocker(options.env)
  return createMySQLBudget({ ...options, dockerPath })
}

/** 默认入口固定已核实的Docker二进制，不修改用户默认context。 */
async function findDocker(env) {
  for (const directory of (env.PATH ?? process.env.PATH).split(':')) {
    const file = join(directory, 'docker')
    try { await access(file, 1); return file } catch { /* 继续查找已安装Docker。 */ }
  }
  throw new Error('未找到Docker命令')
}

/** 预算覆盖完整测试和归属清理，不允许在清理失败后启动下一例。 */
async function runPlatformImplementation({ root, temporary, env, execute, write, assertIdle }) {
  const plan = await discoverPlatformTests({ root, execute, env })
  const browserTests = await discoverBrowserTests({ root, execute, env, expected: plan.browser })
  const name = `twine-check-${randomUUID()}`
  const password = randomBytes(24).toString('hex')
  const adminPassword = randomBytes(24).toString('hex')
  const envFile = join(temporary, 'mysql.env')
  const binary = join(temporary, 'platform')
  const runtimeInit = join(temporary, 'runtime-init')
  const runtimeDirectory = join(temporary, 'runtime')
  const buildInit = join(temporary, 'build-init')
  const buildDirectory = join(temporary, 'build')
  const goCoverage = join(temporary, 'go-coverage.out')
  const webCoverage = join(temporary, 'web-coverage')
  const webBuild = join(temporary, 'web-dist')
  const browserGoCoverage = join(temporary, 'browser-go-coverage')
  const childEnv = { ...env, GOCACHE: env.GOCACHE || join(temporary, 'go-cache'), FORCE_COLOR: '0' }
  // Playwright 向 worker 传 FORCE_COLOR；移除与其冲突的颜色选项，代码告警仍完整保留。
  delete childEnv.NO_COLOR
  const checked = async (command, args, cwd = root) => {
    write(`[PLATFORM_STEP] ${command} ${args.join(' ')}\n`)
    return checkCommand(command, args, { cwd, env: childEnv, timeoutMs: 600000 }, { execute, write })
  }
  await writeFile(envFile, `MYSQL_ROOT_PASSWORD=${adminPassword}\nMYSQL_ROOT_HOST=%\nMYSQL_DATABASE=twine_test\nMYSQL_USER=twine\nMYSQL_PASSWORD=${password}\n`, { mode: 0o600 })
  let started = false
  let runtimeInstallation
  let buildInstallation
  let reports
  const failures = []
  const state = { root, temporary, name, execute, childEnv, checked, browserGoCoverage, password, binary, webBuild, write, adminPassword, runtimeInit, runtimeDirectory, buildInit, buildDirectory }
  try {
    await checked('docker', ['run', '--rm', '--detach', '--name', name, '--env-file', envFile, '--publish', '127.0.0.1::3306', '--health-cmd=mysqladmin ping --host=127.0.0.1 --silent', '--health-interval=1s', '--health-timeout=3s', '--health-retries=100', MYSQL_IMAGE])
    started = true
    await preparePlatform(state)
    runtimeInstallation = state.runtimeInstallation
    buildInstallation = state.buildInstallation
    const templateReports = state.templateReports
    await checked('go', ['vet', '-tags=integration', './...'], join(root, 'server'))
    for (const script of ['contract:check', 'typecheck', 'lint']) await checked('npm', ['run', script], join(root, 'web'))
    await checked('npm', ['run', 'test', '--', '--coverage.reportsDirectory', webCoverage], join(root, 'web'))
    await checked('npm', ['run', 'build', '--', '--outDir', webBuild, '--emptyOutDir'], join(root, 'web'))
    const cleanup = async () => {
      await assertIdle()
      await cleanupDeploymentResources({ runtimeInstallation, buildInstallation, env, execute })
      await cleanupRuntimeResources({ installation: runtimeInstallation, env, execute })
      await cleanupBuildResources({ installation: buildInstallation, env, execute })
    }
    const schemaCommand = async sql => {
      const result = await execute('docker', ['exec', name, 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --execute "$1"', '--', sql], { env, timeoutMs: 30000 })
      if (result.code !== 0) throw new Error('本次隔离schema管理失败')
    }
    await runBrowserCases({ browserTests, childEnv, schemaCommand, cleanup, checked, root, temporary })
    const profiles = await runGoCases({ plan, childEnv, schemaCommand, cleanup, checked, root, temporary })
    await mergeAtomicCoverage(profiles, goCoverage)
    reports = await platformReports({ temporary, checked, browserGoCoverage, goCoverage, root, templateReports, webCoverage })
  } catch (error) {
    failures.push(error)
  } finally {
    await cleanupPlatform({ ...state, env, envFile, started, assertIdle }, failures)
  }
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
  return reports
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const temporary = await mkdtemp(join(tmpdir(), 'twine-platform-'))
  let preserve = false
  try { await runPlatformChecks({ root: process.cwd(), temporary }); process.stdout.write('[PLATFORM_OK] 检查通过\n') }
  catch (error) { preserve = requiresPreservation(error); process.stderr.write(`${error.message}\n`); process.exitCode = 1 }
  finally { if (preserve) process.stderr.write(`运行测试现场保留在 ${temporary}\n`); else await rm(temporary, { recursive: true, force: true }) }
}

/** 验证单个Go覆盖率块及跨报告同起点坐标，不拒绝编译器生成的零长度块。 */
function parseAtomicBlock(line, coordinates) {
  const match = /^(\S+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$/.exec(line)
  if (!match) throw new Error('测试分组覆盖率块格式无效')
  const [, file, startLine, startColumn, endLine, endColumn, statements, count] = match
  const numbers = [startLine, startColumn, endLine, endColumn, statements, count].map(Number)
  if (numbers.some(value => !Number.isSafeInteger(value)) || numbers.slice(0, 4).some(value => value < 1)) throw new Error('测试分组覆盖率数值无效')
  if (numbers[0] > numbers[2] || numbers[0] === numbers[2] && numbers[1] > numbers[3]) throw new Error('测试分组覆盖率坐标无效')
  const location = `${file}:${startLine}.${startColumn},${endLine}.${endColumn}`
  const start = `${file}:${startLine}.${startColumn}`
  if (coordinates.has(start) && coordinates.get(start) !== location) throw new Error('测试分组覆盖率块坐标不一致')
  coordinates.set(start, location)
  return { location, numbers }
}

/** 每个浏览器用例拥有独立schema，进程结束后清理，覆盖率仍写同一GOCOVERDIR。 */
async function runBrowserCases({ browserTests, childEnv, schemaCommand, cleanup, checked, root, temporary }) {
  for (const [index, entry] of browserTests.entries()) {
    const report = join(temporary, `browser-case-${index}.json`)
    const processMarker = join(temporary, `browser-case-${index}.process-unknown`)
    childEnv.TWINE_TEST_PROCESS_UNCONFIRMED_FILE = processMarker
    childEnv.PLAYWRIGHT_JSON_OUTPUT_NAME = report
    childEnv.TWINE_TEST_ARTIFACTS = join(temporary, `browser-case-${index}`)
    await isolatedCase({ env: childEnv, schemaCommand, cleanup: async () => {
      try { await access(processMarker); throw Object.assign(new Error('平台进程退出未确认'), { preserveTemporary: true }) } catch (error) { if (error.code !== 'ENOENT') throw error }
      await cleanup()
    }, action: async () => {
      await checked('npm', ['run', 'test:e2e', '--', `${entry.file.slice(4)}:${entry.line}`, '--workers=1', '--retries=0', '--forbid-only', '--reporter=json'], join(root, 'web'))
      const value = JSON.parse(await readFile(report, 'utf8'))
      if (value.stats?.expected !== 1 || value.stats?.unexpected || value.stats?.skipped || value.stats?.flaky) throw new Error('浏览器单例未完整通过')
    } })
  }
  delete childEnv.PLAYWRIGHT_JSON_OUTPUT_NAME
}

/** 每个物理顶层用例独立执行；纯/元数据测试按包精确分组，所有报告都累加。 */
async function runGoCases({ plan, childEnv, schemaCommand, cleanup, checked, root, temporary }) {
  const reports = []
  for (const [index, group] of goGroups(plan.tests).entries()) {
    const report = join(temporary, `go-case-${index}.out`)
    const pattern = `^(${group.names.join('|')})$`
    await isolatedCase({ env: childEnv, schemaCommand, cleanup, action: async () => {
      const output = await checked('go', ['test', '-json', '-count=1', '-p=1', '-race', '-tags=integration', '-coverpkg=./...', `-run=${pattern}`, `-coverprofile=${report}`, group.pkg], join(root, 'server'))
      verifyGoCompletion(output, group.names)
    } })
    reports.push(report)
  }
  return reports
}

/** 构建工具和模板准备立即登记安装身份，后续失败仍能精确清理。 */
async function preparePlatform(state) {
  const { root, temporary, name, execute, childEnv, checked, browserGoCoverage, password, binary, webBuild, write, adminPassword, runtimeInit, runtimeDirectory, buildInit, buildDirectory } = state
    await waitForDatabase(name, execute, childEnv)
    await mkdir(browserGoCoverage)
    const port = (await checked('docker', ['inspect', '--format={{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}', name])).trim()
    if (!/^\d+$/.test(port)) throw new Error('临时 MySQL 端口无效')
    Object.assign(childEnv, { TWINE_TEST_DATABASE_DSN: `twine:${password}@tcp(127.0.0.1:${port})/twine_test`, TWINE_DATABASE_DSN: `twine:${password}@tcp(127.0.0.1:${port})/twine_test`, TWINE_TEST_BINARY: binary, TWINE_TEST_ARTIFACTS: join(temporary, 'browser'), TWINE_TEST_WEB_DIRECTORY: webBuild })
    if ((await checked('gofmt', ['-l', 'server'])).trim()) throw new Error('Go 格式检查未通过')
    childEnv.GOCOVERDIR = browserGoCoverage
    state.templateReports = await runServiceProfileChecks({ root, temporary, env: childEnv, execute, write, adminDSN: `root:${adminPassword}@tcp(127.0.0.1:${port})/twine_test` })
    childEnv.TWINE_TEST_PROFILE_BINARY = join(temporary, 'service-profile')
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', binary, './cmd/platform'], join(root, 'server'))
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', runtimeInit, './cmd/runtime-init'], join(root, 'server'))
    childEnv.TWINE_REPOSITORY_ROOTS = JSON.stringify([root])
    const runtimeInstallation = state.runtimeInstallation = parseRuntimeInstallation(await checked(runtimeInit, ['--directory', runtimeDirectory]))
    Object.assign(childEnv, { TWINE_RUNTIME_ADAPTER: 'file-secrets-v1', TWINE_RUNTIME_DIRECTORY: runtimeDirectory, TWINE_TEST_RUNTIME_DIRECTORY: runtimeDirectory, TWINE_TEST_DOCKER_CONTEXT: runtimeInstallation.context, TWINE_TEST_DOCKER_DAEMON_ID: runtimeInstallation.daemonId, TWINE_TEST_RUNTIME_INSTALLATION_ID: runtimeInstallation.installationId })
    // 测试准备显式拉取固定工具链；产品启动与构建适配器均不自动拉取。
    await checked('docker', ['pull', BUILD_IMAGE])
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', buildInit, './cmd/build-init'], join(root, 'server'))
    state.buildInstallation = parseBuildInstallation(await checked(buildInit, ['--directory', buildDirectory]))
    Object.assign(childEnv, { TWINE_BUILD_ADAPTER: 'local-go-container-v1', TWINE_BUILD_DIRECTORY: buildDirectory, TWINE_TEST_BUILD_DIRECTORY: buildDirectory, TWINE_DEPLOY_ADAPTER: 'go-mysql-compose-v1' })
}

/** 仅本轮完整覆盖率合并后产生报告。 */
async function platformReports({ temporary, checked, browserGoCoverage, goCoverage, root, templateReports, webCoverage }) {
    const browserProfile = join(temporary, 'browser-go.out')
    await checked('go', ['tool', 'covdata', 'textfmt', `-i=${browserGoCoverage}`, `-o=${browserProfile}`], join(root, 'server'))
    // Sonar 以仓库为基准解析 Go 模块覆盖率路径，显式转换本次报告。
    await mergeGoCoverage([goCoverage, browserProfile, ...templateReports.goCoverageReportPaths], goCoverage)
    const lcov = join(webCoverage, 'lcov.info')
    await writeFile(lcov, (await readFile(lcov, 'utf8')).replace(/^SF:src\//gm, 'SF:web/src/'))
    return { goCoverageReportPaths: [goCoverage], lcovReportPaths: [join(webCoverage, 'lcov.info')] }
}

/** 未知启动或未知进程保留现场；已确认停止才按完整安装身份清理。 */
async function cleanupPlatform(state, failures) {
  const { runtimeInstallation, buildInstallation, env, execute, started, name, envFile, assertIdle } = state
  try { await assertIdle() } catch (error) { error.preserveTemporary = true; failures.push(error) }
  if (failures.some(error => requiresPreservation(error))) return
  const actions = [
    () => cleanupDeploymentResources({ runtimeInstallation, buildInstallation, env, execute }),
    () => cleanupBuildResources({ installation: buildInstallation, env, execute }),
    () => cleanupRuntimeResources({ installation: runtimeInstallation, env, execute }),
    () => cleanupDatabase(started, name, envFile, execute, env)
  ]
  for (const action of actions) {
    try { await action() } catch (error) { error.preserveTemporary = true; failures.push(error); return }
  }
}
