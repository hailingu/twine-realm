/** 三实例测试预算只约束数据库数量，不复制平台构建权限或清理外部资源。 */
import { mkdir, lstat, writeFile, readFile, unlink, rmdir, appendFile } from 'node:fs/promises'
import { join } from 'node:path'
import { constants } from 'node:fs'
import { homedir } from 'node:os'
import { randomUUID } from 'node:crypto'
import { spawn } from 'node:child_process'
import { setTimeout as delay } from 'node:timers/promises'

const ID = /^[a-f0-9]{64}$/

/** 排他锁只由原目录及随机拥有者释放，未知内容不递归清除。 */
export async function acquireBudgetLock(path) {
  await mkdir(path, { mode: 0o700 })
  const identity = await lstat(path); const token = randomUUID(); const owner = join(path, 'owner')
  await writeFile(owner, token, { mode: 0o600, flag: 'wx' })
  return async () => {
    const current = await lstat(path)
    if (!current.isDirectory() || current.ino !== identity.ino || current.dev !== identity.dev || await readFile(owner, 'utf8') !== token) throw new Error('测试预算锁归属改变，保留现场')
    await unlink(owner); await rmdir(path)
  }
}

/** 普查失败不能作为没有数据库的证据。 */
async function checked(execute, command, args, env) {
  const result = await execute(command, args, { env, timeoutMs: 30000 })
  if (result.code !== 0 || typeof result.output !== 'string' || /warning:|level=warn/i.test(result.output)) throw new Error('无法核实测试数据库预算')
  return result.output.trim()
}

/** 固定本机端点，不接受环境覆盖或远程daemon。 */
export async function localDockerIdentity(execute, env) {
  if (env.DOCKER_HOST || env.DOCKER_CONTEXT || env.DOCKER_CONFIG) throw new Error('测试预算不接受Docker环境覆盖')
  const context = await checked(execute, 'docker', ['context', 'show'], env)
  const endpoint = await contextEndpoint(execute, env, context)
  const daemonID = await checked(execute, 'docker', ['--context', context, 'info', '--format', '{{.ID}}'], env)
  if (!daemonID) throw new Error('缺少Docker daemon身份')
  return { context, endpoint, daemonID }
}

/** 明确context必须仍指向同一本机Unix socket。 */
async function contextEndpoint(execute, env, context) {
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(context)) throw new Error('Docker context无效')
  const values = JSON.parse(await checked(execute, 'docker', ['context', 'inspect', context], env))
  if (!Array.isArray(values) || values.length !== 1) throw new Error('Docker端点不唯一')
  const endpoint = values[0].Endpoints?.docker?.Host; const url = new URL(endpoint)
  if (url.protocol !== 'unix:' || url.hostname || !url.pathname.startsWith('/') || url.search || url.hash) throw new Error('仅允许本机Docker')
  return endpoint
}

/** 每次调用复验原daemon，默认context变化不能重定向资源命令。 */
async function verifyIdentity(execute, env, identity) {
  if (await contextEndpoint(execute, env, identity.context) !== identity.endpoint || await checked(execute, 'docker', ['--context', identity.context, 'info', '--format', '{{.ID}}'], env) !== identity.daemonID) throw new Error('原Docker daemon已改变')
}

/** 配置特征和实际进程一起计数，包装镜像不能隐藏mysqld。 */
function databaseConfig(config) {
  if (!config || ![config.Cmd ?? [], config.Entrypoint ?? [], config.Env ?? []].every(Array.isArray)) throw new Error('容器配置无法核实')
  const image = /^(mysql|mariadb)(?::|@|$)/.test((config.Image ?? '').split('/').at(-1))
  const server = [...config.Cmd ?? [], ...config.Entrypoint ?? []].some(value => /(?:^|[\s/])(mysqld|mariadbd)(?:$|\s)/.test(value))
  const initialization = (config.Env ?? []).some(value => /^(MYSQL|MARIADB)_(ROOT_PASSWORD|ROOT_PASSWORD_FILE|ALLOW_EMPTY_ROOT_PASSWORD|RANDOM_ROOT_PASSWORD)(=|$)/.test(value))
  return image || server || initialization || Object.keys(config.ExposedPorts ?? {}).some(key => key.startsWith('3306/'))
}

/** 只读取PID和进程名，不读取数据库连接参数或秘密。 */
function databaseProcesses(value, header = false) {
  const lines = value.trim().split('\n').filter(Boolean)
  if (header && (lines.shift() ?? '').trim().replaceAll(/\s+/g, ' ') !== 'PID COMMAND') throw new Error('容器进程表头不可核实')
  if (header && !lines.length) throw new Error('容器进程表为空')
  const result = new Set()
  for (const line of lines) {
    const fields = line.trim().split(/\s+/)
    const pid = Number(fields[0]); const command = fields.slice(1).join(' ')
    if (!/^[1-9]\d*$/.test(fields[0]) || !Number.isSafeInteger(pid) || !command) throw new Error('进程PID或名称不可核实')
    if (['mysqld', 'mariadbd'].includes(command.split('/').at(-1))) result.add(pid)
  }
  return result
}

/** 宿主PID与cgroup紧邻采样，不在两者之间插入缓慢Docker读取；失败仍整轮重采。 */
async function captureHostGroups(host, { platform = process.platform, readCgroup = pid => readFile('/proc/' + pid + '/cgroup', 'utf8'), readStat = pid => readFile('/proc/' + pid + '/stat', 'utf8') }) {
  const groups = new Map()
  if (platform !== 'linux') return groups
  for (const pid of host) {
    let cgroup
    try { cgroup = await readCgroup(pid) } catch (error) {
      if (error.code !== 'ENOENT') { throw error }
      throw Object.assign(new Error('数据库普查快照变化'), { budgetCode: 'SNAPSHOT_CHANGED', missingKind: await missingProcessKind(pid, readStat) })
    }
    groups.set(pid, new Set(cgroup.split('\n').flatMap(line => line.split(':').slice(2).join(':').split('/'))))
  }
  return groups
}

/** cgroup缺失后的stat只用于固定诊断，不用于减数、判定PID身份或结算旧命令。 */
async function missingProcessKind(pid, readStat) {
  let value
  try { value = await readStat(pid) } catch (error) { return error.code === 'ENOENT' ? 'STAT_MISSING' : 'STAT_UNREADABLE' }
  if (typeof value !== 'string' || value.length > 65536 || !value.startsWith(pid + ' (')) return 'STAT_UNREADABLE'
  const end = value.lastIndexOf(')')
  const fields = value.slice(end + 1).trim().split(/\s+/)
  if (end < 0 || fields.length < 20 || !/^\d+$/.test(fields[19])) return 'STAT_UNREADABLE'
  if (['Z', 'X', 'x'].includes(fields[0])) return 'STAT_EXITED'
  return ['R', 'S', 'D', 'T', 't', 'W', 'I', 'P'].includes(fields[0]) ? 'STAT_LIVE' : 'STAT_UNREADABLE'
}

/** 仅凭本轮已采cgroup证据与核实容器ID精确归属，保留宿主和top计数的较大值。 */
function hostDatabaseCount(host, containers, groups) {
  let count = 0
  const attributed = new Map()
  for (const pid of host) {
    const owners = containers.filter(value => value.databaseCount > 0)
    const segments = groups.get(pid)
    if (!segments) { count++; continue }
    const owner = owners.find(value => segments.has(value.id) || segments.has('docker-' + value.id + '.scope'))
    if (owner) attributed.set(owner.id, (attributed.get(owner.id) ?? 0) + 1)
    else count++
  }
  const additional = containers.reduce((total, value) => total + Math.max(0, (attributed.get(value.id) ?? 0) - value.databaseCount), 0)
  return { hostUnattributed: count, additional }
}

/** 宿主和全部运行容器均占预算，绝不停止无关实例。 */
async function surveySnapshot(execute, env, context, processOptions) {
  const host = databaseProcesses(await checked(execute, 'ps', ['-axo', 'pid=,comm='], env))
  const groups = await captureHostGroups(host, processOptions)
  const docker = args => checked(execute, 'docker', ['--context', context, ...args], env)
  const ids = (await docker(['ps', '--quiet', '--no-trunc'])).split(/\s+/).filter(Boolean)
  if (!ids.every(id => ID.test(id)) || new Set(ids).size !== ids.length) throw new Error('容器列表无效')
  if (!ids.length) {
    const { hostUnattributed } = hostDatabaseCount(host, [], groups)
    return { count: hostUnattributed, hostCandidates: host.size, containerDB: 0, hostUnattributed, running: new Set() }
  }
  const values = JSON.parse(await docker(['inspect', ...ids]))
  if (!Array.isArray(values) || values.length !== ids.length || new Set(values.map(v => v.Id)).size !== ids.length) throw new Error('容器普查不完整')
  const containers = []; let count = 0
  for (const value of values) {
    if (!ids.includes(value.Id)) throw new Error('运行容器身份改变')
    const pids = await containerDatabaseProcesses(value, docker, ids)
    if (pids === null) continue
    const databaseCount = Math.max(databaseConfig(value.Config) ? 1 : 0, pids.size)
    containers.push({ id: value.Id, pids, databaseCount })
    count += databaseCount
  }
  const { hostUnattributed, additional } = hostDatabaseCount(host, containers, groups)
  count += additional
  return { count: count + hostUnattributed, hostCandidates: host.size, containerDB: count, hostUnattributed, running: new Set(containers.map(value => value.id)) }
}

/** PID消失只触发整轮只读重采，最多三次，不重复任何启动或解除旧未知锁。 */
export async function surveyDatabases(execute, env, context, processOptions = {}) {
  let missingKind
  for (let attempt = 0; attempt < 3; attempt++) {
    try { return await surveySnapshot(execute, env, context, processOptions) }
    catch (error) {
      if (error.budgetCode !== 'SNAPSHOT_CHANGED') throw error
      missingKind = error.missingKind
    }
  }
  throw Object.assign(new Error('数据库普查持续变化'), { budgetCode: 'SNAPSHOT_UNSTABLE', missingKind })
}

/** 普通容器可在普查窗口退出；只读同ID复核和新running集合共同证明才跳过。 */
async function confirmedContainerExit(id, docker, originalIDs) {
  const values = JSON.parse(await docker(['inspect', id]))
  if (!Array.isArray(values) || values.length !== 1 || values[0].Id !== id) throw new Error('退出容器身份无法核实')
  if (values[0].State?.Running !== false) return false
  const running = (await docker(['ps', '--quiet', '--no-trunc'])).split(/\s+/).filter(Boolean)
  if (!running.every(value => ID.test(value)) || new Set(running).size !== running.length) throw new Error('退出复核运行集合无效')
  if (running.some(value => !originalIDs.includes(value))) throw new Error('退出复核发现新容器，需重新普查')
  return !running.includes(id)
}

/** top失败只在原容器已证实退出时忽略；未知daemon和仍运行的对象失败关闭。 */
async function containerDatabaseProcesses(value, docker, ids) {
  if (value.State?.Running === false && await confirmedContainerExit(value.Id, docker, ids)) return null
  if (value.State?.Running !== true) throw new Error('运行容器状态无法核实')
  try { return databaseProcesses(await docker(['top', value.Id, '-eo', 'pid,comm']), true) }
  catch (error) {
    if (await confirmedContainerExit(value.Id, docker, ids)) return null
    throw error
  }
}

/** 只允许显式一致context，其他全局端点选项失败关闭。 */
function fixedArguments(args, context) {
  if (args[0] === '--context') {
    if (args[1] !== context) throw new Error('测试命令切换Docker context')
    args = args.slice(2)
  }
  if (!args[0] || args[0].startsWith('-') || args.some(value => /^(--host|--context|--config)(=|$)|^-H/.test(value))) throw new Error('未知Docker全局选项')
  return args
}

/** compose配置是实际候选的来源，不从服务名称猜测数据库。 */
async function composeCandidates(args, docker, running) {
  const action = args.findIndex(value => ['up', 'start', 'restart', 'run'].includes(value))
  if (action < 0) return null
  if (args[action] === 'run') throw new Error('测试不支持临时compose run')
  const prefix = args.slice(0, action)
  const config = JSON.parse(await docker([...prefix, 'config', '--format', 'json']))
  if (!config.services || typeof config.services !== 'object') throw new Error('Compose配置无法核实')
  // 保守计入全部服务，包含depends_on；不因显式只选应用而遗漏数据库。
  const services = Object.keys(config.services)
  let count = 0
  for (const name of services) {
    const service = config.services[name]
    const ids = (await docker([...prefix, 'ps', '--all', '--quiet', name])).split(/\s+/).filter(Boolean)
    if (ids.some(id => !ID.test(id))) throw new Error('Compose容器身份无效')
    if (!databaseConfig({ Image: service.image, Cmd: service.command, Entrypoint: service.entrypoint, Env: Object.keys(service.environment ?? {}) })) continue
    count += ids.length ? ids.filter(id => !running.has(id)).length : 1
  }
  return count
}

/** 对启动原容器核实配置；run按镜像配置和显式参数保守估计。 */
async function candidateCount(args, docker, running) {
  if (args[0] === 'compose') return composeCandidates(args, docker, running)
  const values = args[0] === 'container' ? args.slice(1) : args
  if (['start', 'restart', 'unpause'].includes(values[0])) {
    const names = values.slice(1)
    if (!names.length || names.some(name => name.startsWith('-'))) throw new Error('启动参数无法核实')
    const containers = JSON.parse(await docker(['inspect', ...names]))
    if (containers.length !== names.length) throw new Error('启动容器身份不完整')
    return containers.filter(v => !running.has(v.Id) && databaseConfig(v.Config)).length
  }
  if (values[0] === 'run') {
    // run仅用于门禁元数据库，任何额外run保守预留一个数据库名额。
    return 1
  }
  if (values[0] === 'exec' && values.some(value => /(?:^|\/)(mysqld|mariadbd)$/.test(value))) throw new Error('不允许exec启动额外数据库')
  return null
}

/** 仅确定无写入的资源读取可省去重复发现；context元数据和所有其他命令保持原复验。 */
function directResourceRead(args) {
  if (['info', 'version', 'inspect', 'ps', 'images', 'top', 'logs'].includes(args[0])) return true
  const actions = { container: ['inspect', 'ls', 'logs', 'top'], image: ['inspect', 'ls', 'history'], network: ['inspect', 'ls'], volume: ['inspect', 'ls'] }
  return actions[args[0]]?.includes(args[1]) === true
}

/** 只读快速路径单独核实短context选项，始终向冻结Unix端点发出原读取。 */
function executeResourceRead(args, { execute, env, identity }, options) {
  if (args.some(value => value.startsWith('-c'))) throw new Error('只读命令不能覆写Docker context')
  return execute('docker', ['--host', identity.endpoint, ...args], { ...options, env })
}

/** 短锁覆盖普查、启动及结果核实；未知退出保留锁，禁止后续追加。 */
export async function executeBudgetedDocker(args, { execute, env, identity, startLock }, options = {}) {
  args = fixedArguments(args, identity.context)
  if (directResourceRead(args)) return executeResourceRead(args, { execute, env, identity }, options)
  await verifyIdentity(execute, env, identity)
  if (['swarm', 'service', 'stack'].includes(args[0]) || args[0] === 'compose' && args.some(value => ['watch', 'scale', 'alpha'].includes(value))) throw new Error('测试不支持集群启动入口')
  const docker = values => checked(execute, 'docker', ['--context', identity.context, ...values], env)
  const mayStart = ['run', 'start', 'restart', 'unpause'].includes(args[0]) || args[0] === 'container' && ['run', 'start', 'restart', 'unpause'].includes(args[1]) || args[0] === 'compose' && args.some(value => ['up', 'start', 'restart', 'run'].includes(value))
  if (!mayStart) {
    await candidateCount(args, docker, new Set())
    return execute('docker', ['--context', identity.context, ...args], { ...options, env })
  }
  return executeBudgetedStart(args, { execute, env, identity, startLock }, options, docker)
}

/** 阶段和计数诊断只来自固定枚举及数字，任何外部错误内容均不进入诊断。 */
async function executeBudgetedStart(args, { execute, env, identity, startLock }, options, docker) {
  const release = await waitStartLock(startLock)
  let uncertain = false; let stage = 'pre_survey'; let counts = {}
  try {
    const state = await surveyDatabases(execute, env, identity.context)
    counts = state
    stage = 'candidates'
    const extra = await candidateCount(args, docker, state.running)
    counts = { ...counts, extra }
    if (extra !== null && state.count + extra > 3) throw new Error('本机MySQL最多3实例，未启动额外数据库')
    uncertain = true
    stage = 'start'
    const result = await execute('docker', ['--context', identity.context, ...args], { ...options, env })
    if (result.code !== 0 || result.uncertain === true) throw new Error('Docker启动结果未知，保留预算锁')
    stage = 'post_survey'
    counts = { extra }
    counts = { ...await surveyDatabases(execute, env, identity.context), extra }
    if (counts.count > 3) throw new Error('外部数据库并发变化，停止本轮测试并保留现场')
    uncertain = false
    return result
  } catch (error) {
    if (uncertain) error.preserveTemporary = true
    error.budgetDiagnostic = diagnosticFields(error, stage, counts)
    throw error
  } finally { if (!uncertain) await release() }
}

/** 仅公开固定诊断枚举及有限整数，丢弃原错误、路径、参数和资源身份。 */
function diagnosticFields(error, stage, counts) {
  const code = ['SNAPSHOT_UNSTABLE'].includes(error.budgetCode) ? error.budgetCode : 'BUDGET_CHECK_FAILED'
  const result = { code, stage }
  const missingKind = error.missingKind ?? counts.missingKind
  if (['STAT_MISSING', 'STAT_LIVE', 'STAT_EXITED', 'STAT_UNREADABLE'].includes(missingKind)) result.missingKind = missingKind
  for (const key of ['hostCandidates', 'containerDB', 'hostUnattributed', 'count', 'extra']) {
    if (Number.isSafeInteger(counts[key]) && counts[key] >= 0) result[key] = counts[key]
  }
  return result
}

/** 子进程只输出一行白名单诊断，不暴露任意错误文本。 */
export function budgetDiagnosticLine(error) {
  const value = error?.budgetDiagnostic
  const stage = ['pre_survey', 'candidates', 'start', 'post_survey'].includes(value?.stage) ? value.stage : 'guard'
  return JSON.stringify(diagnosticFields({ budgetCode: value?.code }, stage, value ?? {})) + '\n'
}

/** 诊断sidecar限当前用户私有guard目录和小型普通文件，拒绝符号链接。 */
async function diagnosticFile(directory) {
  const parent = await lstat(directory)
  if (!parent.isDirectory() || parent.uid !== process.getuid() || (parent.mode & 0o777) !== 0o700) throw new Error('诊断目录归属无效')
  const file = join(directory, 'diagnostics.jsonl')
  try {
    const value = await lstat(file)
    if (!value.isFile() || value.uid !== process.getuid() || (value.mode & 0o777) !== 0o600 || value.size > 65536) throw new Error('诊断文件归属或大小无效')
  } catch (error) { if (error.code !== 'ENOENT') throw error }
  return file
}

/** 写入仅经过白名单重新构造的单行诊断，不能写入原错误文本。 */
export async function recordBudgetDiagnostic(directory, error) {
  const file = await diagnosticFile(directory)
  await appendFile(file, budgetDiagnosticLine(error), { mode: 0o600, flag: constants.O_APPEND | constants.O_CREAT | constants.O_WRONLY | constants.O_NOFOLLOW })
}

/** 外层失败报告只读取和再次筛选本轮固定sidecar，不解析其他文件。 */
export async function readBudgetDiagnostics(directory) {
  const file = await diagnosticFile(directory)
  let content
  try { content = await readFile(file, 'utf8') } catch (error) {
    if (error.code === 'ENOENT') { return [] }
    throw error
  }
  return content.split('\n').filter(Boolean).map(line => budgetDiagnosticLine({ budgetDiagnostic: JSON.parse(line) }).trim())
}

/** 本轮并发启动串行化；旧锁不会由新调用擅自移除。 */
async function waitStartLock(path) {
  for (let attempt = 0; attempt < 100; attempt++) {
    try { return await acquireBudgetLock(path) } catch (error) { if (error.code !== 'EEXIST') throw error }
    await delay(50)
  }
  throw Object.assign(new Error('数据库启动预算锁仍占用，需人工核实原命令后才能继续'), { preserveTemporary: true })
}

/** guard保留交互stdin/stdout用于SQL会话，普查输出单独受限收集。 */
export function guardExecutor(dockerPath, inherited = false) {
  return (command, args, options = {}) => new Promise((resolve, reject) => {
    const child = spawn(command === 'docker' ? dockerPath : command, args, { env: options.env, stdio: inherited && !options.timeoutMs ? 'inherit' : ['ignore', 'pipe', 'pipe'] })
    let output = ''; let overflow = false
    const collect = chunk => { output += chunk; if (output.length > 1048576) { overflow = true; child.kill() } }
    child.stdout?.on('data', collect); child.stderr?.on('data', collect)
    const timer = options.timeoutMs ? setTimeout(() => child.kill(), options.timeoutMs) : null
    child.on('error', reject)
    child.on('close', (code, signal) => {
      if (timer) { clearTimeout(timer) }
      resolve({ code, output, uncertain: Boolean(signal) || overflow })
    })
  })
}

/** 整轮排他锁覆盖普查直到归属清理，子进程通过同一固定daemon入口执行。 */
export async function createMySQLBudget({ directory, execute, env, dockerPath, lockPath }) {
  const release = await acquireBudgetLock(lockPath ?? await privateBudgetPath())
  try {
    const identity = await localDockerIdentity(execute, env)
    if ((await surveyDatabases(execute, env, identity.context)).count >= 3) throw new Error('现有数据库已占满3实例预算')
    await mkdir(directory, { mode: 0o700 })
    const startLock = join(directory, 'starting'); const config = { env, identity, startLock }
    const source = '#!' + process.execPath + '\nimport { executeBudgetedDocker, guardExecutor, wrapperBudgetDiagnostic } from ' + JSON.stringify(import.meta.url) + ';\nconst config=' + JSON.stringify({ identity, startLock, directory }) + ';\ntry {const result=await executeBudgetedDocker(process.argv.slice(2),{...config,env:process.env,execute:guardExecutor(' + JSON.stringify(dockerPath) + ',true)});process.exit(result.code ?? 1)}catch(error){process.stderr.write(await wrapperBudgetDiagnostic(config.directory,error));process.exit(1)}\n'
    await writeFile(join(directory, 'docker'), source, { mode: 0o700, flag: 'wx' })
    const childEnv = guardedEnvironment(directory, env)
    const assertIdle = async () => { const unlock = await waitStartLock(startLock); await unlock() }
    return { context: identity.context, daemonID: identity.daemonID, env: childEnv, release: async () => { await assertIdle(); await release() }, assertIdle, readDiagnostics: () => readBudgetDiagnostics(directory), execute: budgetExecutor({ directory, dockerPath, execute, config, childEnv }) }
  } catch (error) { await release(); throw error }
}

/** 调用方环境是完整集合，仅在其原PATH前添加本轮guard，不恢复已删除的父级变量。 */
function guardedEnvironment(directory, env) {
  const paths = (env.PATH ?? '').split(':').filter(path => path && path !== directory)
  return { ...env, PATH: [directory, ...paths].join(':') }
}

/** Docker使用已核实绝对路径，避免受控PATH再次进入guard；所有分支尊重精简环境。 */
function budgetExecutor({ directory, dockerPath, execute, config, childEnv }) {
  const invoke = (command, args, options) => execute(command === 'docker' ? dockerPath : command, args, options)
  return async (command, args, options = {}) => {
    const env = guardedEnvironment(directory, options.env ?? childEnv)
    if (command === 'docker') {
      try { return await executeBudgetedDocker(args, { ...config, env, execute: invoke }, { ...options, env }) }
      catch (error) { return preserveBudgetFailure(directory, error) }
    }
    return execute(command, args, { ...options, env })
  }
}

/** 固定跨进程锁置于用户私有目录；权限或所有者异常不自动修复。 */
export async function privateBudgetPath(directory = join(homedir(), '.twine-realm-test-locks')) {
  try { await mkdir(directory, { mode: 0o700 }) } catch (error) { if (error.code !== 'EEXIST') throw error }
  const value = await lstat(directory)
  if (!value.isDirectory() || value.uid !== process.getuid() || (value.mode & 0o777) !== 0o700) throw new Error('测试锁目录不是当前用户私有目录')
  return join(directory, 'mysql-budget')
}

/** 诊断落盘失败不能替换原未知动作错误或允许清理现场。 */
async function preserveBudgetFailure(directory, error) {
  try { await recordBudgetDiagnostic(directory, error) }
  catch (diagnosticError) { throw Object.assign(new AggregateError([error, diagnosticError], '预算诊断未确认，保留现场'), { preserveTemporary: true }) }
  throw error
}

/** wrapper记录失败也只返回固定诊断，禁止Node输出原异常堆栈或路径。 */
export async function wrapperBudgetDiagnostic(directory, error) {
  try { await recordBudgetDiagnostic(directory, error) }
  catch { return JSON.stringify({ code: 'DIAGNOSTIC_WRITE_FAILED', stage: 'guard' }) + '\n' }
  return budgetDiagnosticLine(error)
}
