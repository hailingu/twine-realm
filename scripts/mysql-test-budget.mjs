/** 三实例测试预算只约束数据库数量，不复制平台构建权限或清理外部资源。 */
import { mkdir, lstat, writeFile, readFile, unlink, rmdir } from 'node:fs/promises'
import { join } from 'node:path'
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

/** Linux仅在精确容器cgroup证明下消重；VM同PID、Mac或已消失PID均不猜测归属。 */
async function hostDatabaseCount(host, containers, { platform = process.platform, readCgroup = pid => readFile('/proc/' + pid + '/cgroup', 'utf8') }) {
  let count = 0
  for (const pid of host) {
    const owners = containers.filter(value => value.pids.has(pid))
    if (platform !== 'linux' || !owners.length) { count++; continue }
    let cgroup
    try { cgroup = await readCgroup(pid) } catch (error) {
      if (error.code !== 'ENOENT') { throw error }
      count++; continue
    }
    const segments = new Set(cgroup.split('\n').flatMap(line => line.split(':').slice(2).join(':').split('/')))
    if (!owners.some(value => segments.has(value.id) || segments.has('docker-' + value.id + '.scope'))) count++
  }
  return count
}

/** 宿主和全部运行容器均占预算，绝不停止无关实例。 */
export async function surveyDatabases(execute, env, context, processOptions = {}) {
  const host = databaseProcesses(await checked(execute, 'ps', ['-axo', 'pid=,comm='], env))
  const docker = args => checked(execute, 'docker', ['--context', context, ...args], env)
  const ids = (await docker(['ps', '--quiet', '--no-trunc'])).split(/\s+/).filter(Boolean)
  if (!ids.every(id => ID.test(id)) || new Set(ids).size !== ids.length) throw new Error('容器列表无效')
  if (!ids.length) return { count: host.size, running: new Set() }
  const values = JSON.parse(await docker(['inspect', ...ids]))
  if (!Array.isArray(values) || values.length !== ids.length || new Set(values.map(v => v.Id)).size !== ids.length) throw new Error('容器普查不完整')
  const containers = []; let count = 0
  for (const value of values) {
    if (!ids.includes(value.Id)) throw new Error('运行容器身份改变')
    const pids = await containerDatabaseProcesses(value, docker, ids)
    if (pids === null) continue
    containers.push({ id: value.Id, pids })
    count += Math.max(databaseConfig(value.Config) ? 1 : 0, pids.size)
  }
  count += await hostDatabaseCount(host, containers, processOptions)
  return { count, running: new Set(containers.map(value => value.id)) }
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
  const release = await waitStartLock(startLock)
  let uncertain = false
  try {
    const state = await surveyDatabases(execute, env, identity.context)
    const extra = await candidateCount(args, docker, state.running)
    if (extra !== null && state.count + extra > 3) throw new Error('本机MySQL最多3实例，未启动额外数据库')
    uncertain = true
    const result = await execute('docker', ['--context', identity.context, ...args], { ...options, env })
    if (result.code !== 0 || result.uncertain === true) throw new Error('Docker启动结果未知，保留预算锁')
    if (extra !== null && (await surveyDatabases(execute, env, identity.context)).count > 3) throw new Error('外部数据库并发变化，停止本轮测试并保留现场')
    uncertain = false
    return result
  } catch (error) {
    if (uncertain) error.preserveTemporary = true
    throw error
  } finally { if (!uncertain) await release() }
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
    const source = '#!' + process.execPath + '\nimport { executeBudgetedDocker, guardExecutor } from ' + JSON.stringify(import.meta.url) + ';\nconst config=' + JSON.stringify({ identity, startLock }) + ';\ntry {const result=await executeBudgetedDocker(process.argv.slice(2),{...config,env:process.env,execute:guardExecutor(' + JSON.stringify(dockerPath) + ',true)});process.exit(result.code ?? 1)}catch{process.stderr.write("数据库测试预算拒绝执行，保留现场\\n");process.exit(1)}\n'
    await writeFile(join(directory, 'docker'), source, { mode: 0o700, flag: 'wx' })
    const childEnv = guardedEnvironment(directory, env)
    const assertIdle = async () => { const unlock = await waitStartLock(startLock); await unlock() }
    return { context: identity.context, daemonID: identity.daemonID, env: childEnv, release: async () => { await assertIdle(); await release() }, assertIdle, execute: budgetExecutor({ directory, dockerPath, execute, config, childEnv }) }
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
  return (command, args, options = {}) => {
    const env = guardedEnvironment(directory, options.env ?? childEnv)
    if (command === 'docker') return executeBudgetedDocker(args, { ...config, env, execute: invoke }, { ...options, env })
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
