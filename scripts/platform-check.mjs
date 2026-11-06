/** 在独立 MySQL 和临时产物目录验证平台；任何失败都清理本任务拥有的容器。 */
import { randomBytes, randomUUID } from 'node:crypto'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
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
export async function runPlatformChecks({ root, temporary, env = process.env, execute = runCommand, write = message => process.stdout.write(message) }) {
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
  try {
    await checked('docker', ['run', '--rm', '--detach', '--name', name, '--env-file', envFile, '--publish', '127.0.0.1::3306', '--health-cmd=mysqladmin ping --host=127.0.0.1 --silent', '--health-interval=1s', '--health-timeout=3s', '--health-retries=100', MYSQL_IMAGE])
    started = true
    await waitForDatabase(name, execute, childEnv)
    await mkdir(browserGoCoverage)
    const port = (await checked('docker', ['inspect', '--format={{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}', name])).trim()
    if (!/^\d+$/.test(port)) throw new Error('临时 MySQL 端口无效')
    Object.assign(childEnv, { TWINE_TEST_DATABASE_DSN: `twine:${password}@tcp(127.0.0.1:${port})/twine_test`, TWINE_DATABASE_DSN: `twine:${password}@tcp(127.0.0.1:${port})/twine_test`, TWINE_TEST_BINARY: binary, TWINE_TEST_ARTIFACTS: join(temporary, 'browser'), TWINE_TEST_WEB_DIRECTORY: webBuild })
    if ((await checked('gofmt', ['-l', 'server'])).trim()) throw new Error('Go 格式检查未通过')
    childEnv.GOCOVERDIR = browserGoCoverage
    const templateReports = await runServiceProfileChecks({ root, temporary, env: childEnv, execute, write, adminDSN: `root:${adminPassword}@tcp(127.0.0.1:${port})/twine_test` })
    childEnv.TWINE_TEST_PROFILE_BINARY = join(temporary, 'service-profile')
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', binary, './cmd/platform'], join(root, 'server'))
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', runtimeInit, './cmd/runtime-init'], join(root, 'server'))
    childEnv.TWINE_REPOSITORY_ROOTS = JSON.stringify([root])
    runtimeInstallation = parseRuntimeInstallation(await checked(runtimeInit, ['--directory', runtimeDirectory]))
    Object.assign(childEnv, { TWINE_RUNTIME_ADAPTER: 'file-secrets-v1', TWINE_RUNTIME_DIRECTORY: runtimeDirectory, TWINE_TEST_RUNTIME_DIRECTORY: runtimeDirectory, TWINE_TEST_DOCKER_CONTEXT: runtimeInstallation.context, TWINE_TEST_DOCKER_DAEMON_ID: runtimeInstallation.daemonId, TWINE_TEST_RUNTIME_INSTALLATION_ID: runtimeInstallation.installationId })
    // 测试准备显式拉取固定工具链；产品启动与构建适配器均不自动拉取。
    await checked('docker', ['pull', BUILD_IMAGE])
    await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', buildInit, './cmd/build-init'], join(root, 'server'))
    buildInstallation = parseBuildInstallation(await checked(buildInit, ['--directory', buildDirectory]))
    Object.assign(childEnv, { TWINE_BUILD_ADAPTER: 'local-go-container-v1', TWINE_BUILD_DIRECTORY: buildDirectory, TWINE_TEST_BUILD_DIRECTORY: buildDirectory, TWINE_DEPLOY_ADAPTER: 'go-mysql-compose-v1' })
    await checked('go', ['vet', '-tags=integration', './...'], join(root, 'server'))
    for (const script of ['contract:check', 'typecheck', 'lint']) await checked('npm', ['run', script], join(root, 'web'))
    await checked('npm', ['run', 'test', '--', '--coverage.reportsDirectory', webCoverage], join(root, 'web'))
    await checked('npm', ['run', 'build', '--', '--outDir', webBuild, '--emptyOutDir'], join(root, 'web'))
    await checked('npm', ['run', 'test:e2e'], join(root, 'web'))
    // 先结束浏览器拥有者并清理其一次性资源，再执行会构造旧schema的隔离迁移夹具。
    await cleanupDeploymentResources({ runtimeInstallation, buildInstallation, env, execute })
    await cleanupRuntimeResources({ installation: runtimeInstallation, env, execute })
    await cleanupBuildResources({ installation: buildInstallation, env, execute })
    await checked('go', ['test', '-count=1', '-race', '-tags=integration', '-coverpkg=./...', `-coverprofile=${goCoverage}`, './...'], join(root, 'server'))
    const browserProfile = join(temporary, 'browser-go.out')
    await checked('go', ['tool', 'covdata', 'textfmt', `-i=${browserGoCoverage}`, `-o=${browserProfile}`], join(root, 'server'))
    // Sonar 以仓库为基准解析 Go 模块覆盖率路径，显式转换本次报告。
    await mergeGoCoverage([goCoverage, browserProfile, ...templateReports.goCoverageReportPaths], goCoverage)
    const lcov = join(webCoverage, 'lcov.info')
    await writeFile(lcov, (await readFile(lcov, 'utf8')).replace(/^SF:src\//gm, 'SF:web/src/'))
    reports = { goCoverageReportPaths: [goCoverage], lcovReportPaths: [join(webCoverage, 'lcov.info')] }
  } catch (error) {
    failures.push(error)
  } finally {
    try { await cleanupDeploymentResources({ runtimeInstallation, buildInstallation, env, execute }) }
    catch (error) { failures.push(error) }
    try { await cleanupBuildResources({ installation: buildInstallation, env, execute }) }
    catch (error) { failures.push(error) }
    try { await cleanupRuntimeResources({ installation: runtimeInstallation, env, execute }) }
    catch (error) { failures.push(error) }
    try { await cleanupDatabase(started, name, envFile, execute, env) }
    catch (error) { failures.push(error) }
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
