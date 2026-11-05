/** Sonar 门禁入口：绑定本次扫描任务，等待服务端质量结果并保护凭据。 */
import { spawn } from 'node:child_process'
import { access, mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const DEFAULT_GATE_TIMEOUT_SECONDS = 300
const MAX_GATE_TIMEOUT_SECONDS = 3600
const REQUEST_TIMEOUT_MS = 15000
const COMMAND_TIMEOUT_MS = 15 * 60 * 1000
const MAX_TIMER_DELAY_MS = 2147483647
const MAX_COMMAND_OUTPUT_BYTES = 32 * 1024 * 1024

/** 校验服务地址和环境凭据，拒绝把缺失配置当作默认授权。 */
export function loadSonarSettings(env) {
  const token = env.SONAR_TOKEN || env.TWINE_REALM_SONAR_TOKEN
  const timeout = Number(env.SONAR_GATE_TIMEOUT_SECONDS || DEFAULT_GATE_TIMEOUT_SECONDS)
  let host
  try { host = new URL(env.SONAR_HOST_URL) } catch { throw new Error('[SONAR_CONFIG] 缺少有效的 SONAR_HOST_URL。') }
  if (!['http:', 'https:'].includes(host.protocol) || host.username || host.password || host.search || host.hash) {
    throw new Error('[SONAR_CONFIG] 服务地址必须是没有凭据、查询或片段的 HTTP(S) 地址。')
  }
  if (!token || !Number.isInteger(timeout) || timeout < 1 || timeout > MAX_GATE_TIMEOUT_SECONDS) {
    throw new Error('[SONAR_CONFIG] 缺少 SONAR_TOKEN / TWINE_REALM_SONAR_TOKEN，或等待时间超出有效范围。')
  }
  host.pathname = `${host.pathname.replace(/\/$/, '')}/`
  return { host: host.href, token, timeout, scanner: env.SONAR_SCANNER_BIN || 'sonar-scanner' }
}

/** 输出子进程诊断前隐藏本次使用的认证值。 */
export function redactOutput(output, token) {
  return token ? String(output).split(token).join('[已隐藏]') : String(output)
}

/** 终止独立进程组，包括父进程已退出但仍持有输出管道的后代。 */
function terminateProcessGroup(child) {
  if (!child.pid) return ''
  try { process.kill(-child.pid, 'SIGKILL') } catch (error) {
    if (error.code !== 'ESRCH') return `[QUALITY_TERMINATION_FAILED] 无法终止进程组：${error.code || 'UNKNOWN'}。\n`
  }
  return ''
}

/** 执行受时限约束的 POSIX 进程组，捕获输出、超时和启动失败。 */
export function runCommand(command, args, configuration = {}) {
  const { timeoutMs = COMMAND_TIMEOUT_MS, ...spawnConfiguration } = configuration
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > MAX_TIMER_DELAY_MS) {
    return Promise.reject(new Error('[QUALITY_COMMAND_CONFIG] 命令超时必须是有效的正整数毫秒值。'))
  }
  if (process.platform === 'win32') {
    return Promise.reject(new Error('[QUALITY_PROCESS_UNSUPPORTED] 当前门禁要求支持 POSIX 进程组的运行环境。'))
  }
  return new Promise(resolveResult => {
    const child = spawn(command, args, { ...spawnConfiguration, detached: true, stdio: ['ignore', 'pipe', 'pipe'] })
    const chunks = []
    let bytes = 0
    let failed = false
    const stop = diagnostic => {
      if (failed) return
      failed = true
      chunks.push(Buffer.from(`\n${diagnostic}\n${terminateProcessGroup(child)}`))
    }
    const timer = setTimeout(() => stop('[QUALITY_COMMAND_TIMEOUT] 命令超过执行时限。'), timeoutMs)
    const interrupt = () => stop('[QUALITY_COMMAND_CANCELLED] 外层进程中断，已停止命令进程组。')
    const interruptSignals = ['SIGINT', 'SIGTERM']
    for (const signal of interruptSignals) process.once(signal, interrupt)
    const collect = chunk => {
      bytes += chunk.length
      if (bytes > MAX_COMMAND_OUTPUT_BYTES) { stop('[QUALITY_OUTPUT_LIMIT] 命令输出超过大小限制。'); return }
      chunks.push(chunk)
    }
    child.stdout.on('data', collect)
    child.stderr.on('data', collect)
    child.once('error', () => { failed = true })
    child.once('close', code => {
      clearTimeout(timer)
      for (const signal of interruptSignals) process.removeListener(signal, interrupt)
      resolveResult({ code: failed ? 1 : code ?? 1, output: Buffer.concat(chunks).toString('utf8') })
    })
  })
}

/** 在可信服务地址上查询 JSON，不向扫描报告提供的任意地址发送认证头。 */
async function requestJson(settings, path, fetchImpl) {
  let response
  try {
    response = await fetchImpl(new URL(path, settings.host), {
      headers: { Authorization: `Bearer ${settings.token}` }, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS)
    })
    if (!response.ok) throw new Error('HTTP')
    return await response.json()
  } catch { throw new Error('[SONAR_API_FAILED] 无法确认本次分析结果。') }
}

/** 读取扫描器生成的项目与计算任务标识，拒绝缺失或错配报告。 */
async function readTask(reportPath, projectKey) {
  let content
  try { content = await readFile(reportPath, 'utf8') } catch { throw new Error('[SONAR_REPORT_MISSING] 本次扫描未生成任务报告。') }
  const values = Object.fromEntries(content.split(/\r?\n/).filter(line => line.includes('=')).map(line => {
    const index = line.indexOf('=')
    return [line.slice(0, index), line.slice(index + 1)]
  }))
  if (values.projectKey !== projectKey || !values.ceTaskId) throw new Error('[SONAR_REPORT_INVALID] 扫描任务与项目不匹配。')
  return values.ceTaskId
}

/** 查询本次计算任务及其分析门槛；任何未知状态均不能通过。 */
async function verifyAnalysis(settings, projectKey, taskId, fetchImpl) {
  const { task } = await requestJson(settings, `api/ce/task?id=${encodeURIComponent(taskId)}`, fetchImpl)
  if (task?.status !== 'SUCCESS' || !task.analysisId || task.componentKey !== projectKey) {
    throw new Error('[SONAR_TASK_FAILED] 本次计算任务尚未成功或归属不匹配。')
  }
  const { projectStatus } = await requestJson(settings, `api/qualitygates/project_status?analysisId=${encodeURIComponent(task.analysisId)}`, fetchImpl)
  if (projectStatus?.status !== 'OK') throw new Error(`[SONAR_GATE_REJECTED] 本次 Quality Gate 未通过：${projectStatus?.status || 'UNKNOWN'}。`)
  const issues = await requestJson(settings, `api/issues/search?componentKeys=${encodeURIComponent(projectKey)}&resolved=false&ps=1`, fetchImpl)
  if (issues.total !== 0) throw new Error(`[SONAR_OPEN_ISSUES] 存在 ${issues.total ?? '未知数量'} 项未解决代码问题，必须清零后才能提交。`)
  return { projectKey, taskId, analysisId: task.analysisId, status: projectStatus.status, conditions: projectStatus.conditions || [], openIssues: 0 }
}

/** 只传递调用方本次提供且实际存在的覆盖率报告，不猜测或回退历史报告。 */
async function coverageParameters(root, flag, reportPaths, label) {
  if (reportPaths === undefined) return []
  const reports = reportPaths.map(path => resolve(root, path))
  for (const report of reports) {
    try { await access(report) } catch {
      throw new Error(`[SONAR_COVERAGE_MISSING] 指定的本次${label}报告不存在或不可读。`)
    }
  }
  return [`-D${flag}=${reports.join(',')}`]
}

/** 校验本次 Java 构建目录并生成扫描参数；平台尚无 Java 源码时允许不提供。 */
async function javaParameters(javaBuildDirectories) {
  if (!javaBuildDirectories || javaBuildDirectories.length === 0) return []
  const javaBinaries = javaBuildDirectories.map(directory => join(directory, 'classes'))
  const javaTestBinaries = javaBuildDirectories.map(directory => join(directory, 'test-classes'))
  // 每个目录的本次构建产物都必须真实存在，任何缺失都不能退回历史 class。
  for (const binaries of [...javaBinaries, ...javaTestBinaries]) {
    try { await access(binaries) } catch { throw new Error('[SONAR_JAVA_MISSING] 指定的 Java 编译产物不存在；请先完成对应构建。') }
  }
  return [`-Dsonar.java.binaries=${javaBinaries.join(',')}`, `-Dsonar.java.test.binaries=${javaTestBinaries.join(',')}`]
}

/** 运行扫描并验证对应分析；临时元数据在成功或失败后都会清理。 */
export async function runSonarGate({
  root = process.cwd(),
  javaBuildDirectories = [],
  javaCoverageReportPaths, lcovReportPaths, goCoverageReportPaths,
  env = process.env, runCommand: execute = runCommand, fetchImpl = fetch
} = {}) {
  const settings = loadSonarSettings(env)
  const properties = await readFile(join(root, 'sonar-project.properties'), 'utf8')
  const projectKey = /^sonar\.projectKey\s*=\s*(\S+)\s*$/m.exec(properties)?.[1]
  if (!projectKey) throw new Error('[SONAR_CONFIG] 项目配置缺少 projectKey。')
  const coverage = [
    ...await coverageParameters(root, 'sonar.javascript.lcov.reportPaths', lcovReportPaths, 'LCOV'),
    ...await coverageParameters(root, 'sonar.coverage.jacoco.xmlReportPaths', javaCoverageReportPaths, 'JaCoCo'),
    ...await coverageParameters(root, 'sonar.go.coverage.reportPaths', goCoverageReportPaths, 'Go')
  ]
  const temporary = await mkdtemp(join(tmpdir(), 'twine-sonar-'))
  try {
    const reportPath = join(temporary, 'report-task.txt')
    const scannerEnv = Object.fromEntries(Object.entries(env).filter(([key]) => !/(TOKEN|PASSWORD|SECRET|API_KEY)/i.test(key)))
    Object.assign(scannerEnv, { SONAR_TOKEN: settings.token, SONAR_HOST_URL: settings.host })
    const result = await execute(settings.scanner, [
      '-Dsonar.qualitygate.wait=true', `-Dsonar.qualitygate.timeout=${settings.timeout}`,
      ...await javaParameters(javaBuildDirectories),
      `-Dsonar.working.directory=${join(temporary, 'work')}`, `-Dsonar.scanner.metadataFilePath=${reportPath}`,
      ...coverage
    ], { cwd: root, env: scannerEnv, timeoutMs: COMMAND_TIMEOUT_MS + settings.timeout * 1000 })
    const output = redactOutput(result.output || '', settings.token)
    if (result.code !== 0) throw new Error(`[SONAR_SCAN_FAILED] 扫描或服务端门禁失败。\n${output}`)
    const taskId = await readTask(reportPath, projectKey)
    const analysis = await verifyAnalysis(settings, projectKey, taskId, fetchImpl)
    return { ...analysis, output }
  } finally { await rm(temporary, { recursive: true, force: true }) }
}

/** 命令行仅输出脱敏诊断和实际分析标识。 */
async function main() {
  try {
    process.stdout.write('[SONAR_GATE] 开始扫描并等待服务端 Quality Gate。\n')
    const { output, ...result } = await runSonarGate()
    process.stdout.write(`${output}\n[SONAR_GATE_OK] ${JSON.stringify(result)}\n`)
  } catch (error) {
    process.stderr.write(`${redactOutput(error.message, process.env.SONAR_TOKEN || process.env.TWINE_REALM_SONAR_TOKEN)}\n`)
    process.exitCode = 1
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main()
