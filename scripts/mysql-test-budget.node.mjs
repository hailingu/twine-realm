/** 所有预算测试使用假执行器，不启动Docker或数据库。 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, rm, readFile, writeFile, rename, mkdir, access, chmod, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { acquireBudgetLock, localDockerIdentity, surveyDatabases, executeBudgetedDocker, createMySQLBudget, privateBudgetPath, budgetDiagnosticLine, recordBudgetDiagnostic, readBudgetDiagnostics } from './mysql-test-budget.mjs'
const identity = { context: 'local', endpoint: 'unix:///local.sock', daemonID: 'original-daemon' }
const ids = ['a', 'b', 'c'].map(value => value.repeat(64))

/** 临时目录仅由本测试建立和清理。 */
async function temporary(t) { const directory = await mkdtemp(join(tmpdir(), 'budget-three-')); t.after(() => rm(directory, { recursive: true, force: true })); return directory }

/** 假Docker记录启动，不创建任何外部资源。 */
function fake(initial = 0) {
  const state = { count: initial, starts: 0, changed: false, unknown: false }
  state.execute = async (command, raw) => {
    const args = ['--context', '--host'].includes(raw[0]) ? raw.slice(2) : raw
    const result = output => ({ code: 0, output })
    if (command === 'ps') return result('1 /sbin/launchd\n')
    if (args[0] === 'context') return result(args[1] === 'show' ? 'local' : JSON.stringify([{ Endpoints: { docker: { Host: identity.endpoint } } }]))
    if (args[0] === 'info') return result(state.changed ? 'other' : identity.daemonID)
    if (args[0] === 'ps') return result(ids.slice(0, state.count).join('\n'))
    if (args[0] === 'inspect') return result(JSON.stringify(args.slice(1).map(Id => ({ Id, Config: { Image: 'mysql', Cmd: ['mysqld'] }, State: { Running: ids.indexOf(Id) < state.count } }))))
    if (args[0] === 'top') return result('PID COMMAND\n' + (ids.indexOf(args[1]) + 101) + ' mysqld\n')
    if (args[0] === 'compose' && args.includes('config')) return result(JSON.stringify({ services: { app: { image: 'app', depends_on: { db: {} } }, db: { image: 'mysql' } } }))
    if (args[0] === 'compose' && args.includes('ps')) return result('')
    state.starts++; state.count++
    return state.unknown ? { code: null, output: '', uncertain: true } : result(ids[state.count - 1] ?? '')
  }
  return state
}

/** 每例持有独立短锁路径，固定身份无需真实Docker。 */
async function settings(t, state) { return { execute: state.execute, env: {}, identity, startLock: join(await temporary(t), 'starting') } }

test('预算锁互斥且不删除替换目录或未知文件', async t => {
  const directory = await temporary(t); const path = join(directory, 'lock'); const release = await acquireBudgetLock(path)
  await assert.rejects(acquireBudgetLock(path))
  await rename(path, path + '-original'); await mkdir(path); await writeFile(join(path, 'owner'), 'foreign')
  await assert.rejects(release()); assert.equal(await readFile(join(path, 'owner'), 'utf8'), 'foreign')
})

test('本机固定context可核实，环境覆写和daemon变更失败关闭', async t => {
  const state = fake(); assert.deepEqual(await localDockerIdentity(state.execute, {}), identity)
  await assert.rejects(localDockerIdentity(state.execute, { DOCKER_HOST: 'ssh://remote' }))
  state.changed = true
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], await settings(t, state)))
  assert.equal(state.starts, 0)
})

test('达到三实例前允许第三个，第四个在启动之前拒绝', async t => {
  const state = fake(2); const config = await settings(t, state)
  await executeBudgetedDocker(['run', 'mysql'], config)
  assert.equal(state.starts, 1)
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config))
  assert.equal(state.starts, 1)
})

test('并发启动检查和动作串行，最多一个调用取得最后名额', async t => {
  const state = fake(2); const config = await settings(t, state)
  const results = await Promise.allSettled([executeBudgetedDocker(['run', 'mysql'], config), executeBudgetedDocker(['run', 'mysql'], config)])
  assert.equal(results.filter(value => value.status === 'fulfilled').length, 1)
  assert.equal(state.starts, 1)
})

test('compose应用依赖数据库计入预算，未知compose run拒绝', async t => {
  const state = fake(3); const config = await settings(t, state)
  await assert.rejects(executeBudgetedDocker(['compose', '--file', 'test.json', 'up', 'app'], config))
  await assert.rejects(executeBudgetedDocker(['compose', 'run', 'app'], config))
  assert.equal(state.starts, 0)
})

test('启动别名和context覆写不能绕过预算，直接mysqld入口拒绝', async t => {
  const state = fake(3); const config = await settings(t, state)
  for (const args of [['container', 'start', 'd'.repeat(64)], ['restart', 'd'.repeat(64)], ['--context', 'remote', 'run', 'mysql'], ['exec', ids[0], '/usr/sbin/mysqld'], ['service', 'create', 'mysql']]) await assert.rejects(executeBudgetedDocker(args, config))
  assert.equal(state.starts, 0)
})

test('未知启动结果保留短锁，不以单次inspect认定旧命令已结束', async t => {
  const state = fake(); state.unknown = true; const config = await settings(t, state)
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config), error => error.preserveTemporary === true)
  await access(join(config.startLock, 'owner'))
})

test('普查容器进程失败关闭，不将失败当零实例', async () => {
  const state = fake(1); const execute = (command, args, options) => args.includes('top') ? { code: 1, output: '' } : state.execute(command, args, options)
  await assert.rejects(surveyDatabases(execute, {}, 'local'))
})

test('整轮预算安装guard并保留固定context与清理前idle接口', async t => {
  const directory = await temporary(t); const state = fake()
  const budget = await createMySQLBudget({ directory: join(directory, 'guard'), execute: state.execute, env: { PATH: '/fake/bin' }, dockerPath: '/fake/docker', lockPath: join(directory, 'platform-lock') })
  assert.match(budget.env.PATH, /guard:/); assert.equal(budget.context, 'local')
  await budget.assertIdle(); await budget.release()
})

test('启动执行器抛错同样保留短锁，不能误判为启动前失败', async t => {
  const state = fake(); const config = await settings(t, state); const execute = config.execute
  config.execute = (command, args, options) => args.includes('run') ? Promise.reject(new Error('未知传输结果')) : execute(command, args, options)
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config), error => error.preserveTemporary === true)
  await access(join(config.startLock, 'owner'))
})

test('元库加两项目库时业务应用和mysql客户端不误占第四名额', async t => {
  const state = fake(3); const config = await settings(t, state); const base = config.execute; let started = false
  config.execute = async (command, args, options) => {
    if (args.includes('inspect') && args.includes('application')) return { code: 0, output: JSON.stringify([{ Id: 'd'.repeat(64), Config: { Image: 'service-candidate', Cmd: ['serve'], Env: ['DB_HOST=tr-mysql-original', 'DB_PORT=3306', 'MYSQL_HOST=tr-mysql-original', 'DRIVER=mysql'] } }]) }
    if (args.includes('start')) { started = true; return { code: 0, output: '' } }
    return base(command, args, options)
  }
  await executeBudgetedDocker(['start', 'application'], config)
  assert.equal(started, true)
  const execute = async (command, args, options) => args.includes('inspect') ? { code: 0, output: JSON.stringify([{ Id: ids[0], State: { Running: true }, Config: { Image: 'client', Cmd: ['mysql'], Env: ['MYSQL_HOST=database'] } }]) } : args.includes('top') ? { code: 0, output: 'PID COMMAND\n101 mysql\n' } : fake(1).execute(command, args, options)
  assert.equal((await surveyDatabases(execute, {}, 'local')).count, 0)
})

test('启动CLI非零不能证明daemon未执行，保留有效预算锁', async t => {
  const state = fake(); const config = await settings(t, state); const base = config.execute
  config.execute = (command, args, options) => args.includes('run') ? { code: 1, output: 'connection closed' } : base(command, args, options)
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config), error => error.preserveTemporary === true)
  await access(join(config.startLock, 'owner'))
})


test('默认预算根必须为本用户0700普通目录，公共权限或symlink不接管', async t => {
  const root = await temporary(t); const path = join(root, 'private')
  assert.equal(await privateBudgetPath(path), join(path, 'mysql-budget'))
  await chmod(path, 0o755); await assert.rejects(privateBudgetPath(path))
  const link = join(root, 'link'); await symlink(path, link)
  await assert.rejects(privateBudgetPath(link))
})

test('启动成功但事后普查失败或超额均保留短锁和现场标记', async t => {
  for (const mode of ['failed', 'overflow']) {
    const state = fake(2); const config = await settings(t, state); const base = config.execute
    config.execute = (command, args, options) => {
      if (state.starts && command === 'ps') return { code: mode === 'failed' ? 1 : 0, output: mode === 'failed' ? '' : '999 mysqld\n' }
      return base(command, args, options)
    }
    await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config), error => error.preserveTemporary === true)
    await access(join(config.startLock, 'owner'))
  }
})

/** Linux原生daemon与VM的PID相等不代表同一进程，必须另外提供精确cgroup归属。 */
function linuxSurvey(host) {
  const base = fake(2).execute
  return (command, args, options) => command === 'ps' ? { code: 0, output: host } : args.includes('top') ? { code: 0, output: 'PID COMMAND\n' + (args.includes(ids[0]) ? '101' : '102') + ' mysqld\n' } : base(command, args, options)
}

test('Linux宿主和容器同PID仅凭精确cgroup归属消重，真正宿主库仍计入', async () => {
  const execute = linuxSurvey('101 mysqld\n102 mysqld\n103 /usr/sbin/mysqld\n')
  const readCgroup = async pid => '0::/system.slice/docker-' + ids[pid - 101] + '.scope\n'
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup })).count, 3)
})

test('Linux VM的PID碰撞或Mac同PID不消重，归属读取失败关闭', async () => {
  const execute = linuxSurvey('101 mysqld\n102 mysqld\n')
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: async () => '0::/user.slice/session.scope\n' })).count, 4)
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'darwin', readCgroup: async () => { throw new Error('Mac不应读取proc') } })).count, 4)
  await assert.rejects(surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: async () => { throw Object.assign(new Error('权限不足'), { code: 'EACCES' }) } }))
})

test('容器ID前缀和退出后PID缺失不作为消重证明', async () => {
  const execute = linuxSurvey('101 mysqld\n102 mysqld\n')
  const prefix = async pid => '0::/docker/' + ids[pid - 101] + 'extra\n'
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: prefix })).count, 4)
  const missing = async () => { throw Object.assign(new Error('进程已退出'), { code: 'ENOENT' }) }
  await assert.rejects(surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: missing }), error => error.budgetCode === 'SNAPSHOT_UNSTABLE')
})

test('PID解析拒绝混合数字及越界值，普通含空格进程名不误计', async () => {
  const base = fake().execute
  for (const value of ['0 mysqld', '12x mysqld', '9007199254740992 mysqld', '123']) {
    const execute = (command, args, options) => command === 'ps' ? { code: 0, output: value } : base(command, args, options)
    await assert.rejects(surveyDatabases(execute, {}, 'local'))
  }
  const execute = (command, args, options) => command === 'ps' ? { code: 0, output: '  123\t/Applications/Normal App\n 124  /usr/sbin/mysqld  \n' } : base(command, args, options)
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: async () => '0::/user.slice/host' })).count, 1)
})

/** 普查窗口内退出的原容器仍可inspect，后续running集合能明确证实退出。 */
function exitingFixture(mode) {
  let lists = 0; let inspections = 0; const base = fake(1).execute
  return (command, raw, options) => {
    const args = ['--context', '--host'].includes(raw[0]) ? raw.slice(2) : raw
    if (command === 'docker' && args[0] === 'ps') return { code: 0, output: ++lists === 1 || mode === 'still-running' ? ids[0] : mode === 'new-container' ? ids[1] : '' }
    if (args[0] === 'inspect') {
      inspections++
      if (mode === 'daemon-error' && inspections > 1) return { code: 1, output: '' }
      return { code: 0, output: JSON.stringify([{ Id: mode === 'replaced' && inspections > 1 ? ids[1] : ids[0], Config: { Image: 'build' }, State: { Running: inspections === 1 && mode !== 'already-exited' } }]) }
    }
    if (args[0] === 'top') return { code: 1, output: '' }
    return base(command, raw, options)
  }
}

test('普查时普通容器退出可由同ID退出状态及新running集合证实，不误保留', async () => {
  for (const mode of ['already-exited', 'top-failed']) {
    const result = await surveyDatabases(exitingFixture(mode), {}, 'local')
    assert.equal(result.count, 0); assert.equal(result.running.size, 0)
  }
})

test('top失败不能掩盖daemon故障、身份替换或仍在running集合', async () => {
  for (const mode of ['daemon-error', 'replaced', 'still-running', 'new-container']) await assert.rejects(surveyDatabases(exitingFixture(mode), {}, 'local'))
})

test('调用方精简env是完整集合，不重新注入颜色或父级秘密，PATH仍受控', async t => {
  const root = await temporary(t); const state = fake(); const calls = []
  const execute = (command, args, options) => {
    calls.push({ command, args, env: options?.env })
    if (command === 'node') return { code: 0, output: '' }
    return state.execute(command === '/fake/docker' ? 'docker' : command, args, options)
  }
  const guard = join(root, 'guard')
  const budget = await createMySQLBudget({ directory: guard, execute, env: { PATH: '/parent/bin', NO_COLOR: '1', SYNTHETIC_SECRET: 'test-only' }, dockerPath: '/fake/docker', lockPath: join(root, 'lock') })
  const env = { PATH: '/caller/bin', FORCE_COLOR: '1' }
  await budget.execute('node', ['test'], { env })
  const child = calls.at(-1).env
  assert.equal(child.NO_COLOR, undefined); assert.equal(child.SYNTHETIC_SECRET, undefined)
  assert.equal(child.PATH, guard + ':/caller/bin'); assert.equal(child.FORCE_COLOR, '1')
  await budget.execute('docker', ['ps', '--quiet', '--no-trunc'], { env })
  const docker = calls.at(-1)
  assert.equal(docker.env.NO_COLOR, undefined); assert.equal(docker.env.SYNTHETIC_SECRET, undefined)
  assert.equal(docker.command, '/fake/docker'); assert.deepEqual(docker.args.slice(0, 2), ['--host', identity.endpoint])
  await budget.execute('node', ['test'], { env: {} })
  assert.deepEqual(calls.at(-1).env, { PATH: guard })
  await budget.execute('node', ['test'])
  assert.equal(calls.at(-1).env.NO_COLOR, '1')
  assert.equal(calls.at(-1).env.PATH, guard + ':/parent/bin')
  await budget.release()
})

test('资源只读仅一次固定Unix endpoint调用，不随context重定向', async t => {
  const state = fake(); const config = await settings(t, state); const calls = []
  config.execute = async (command, args) => { calls.push({ command, args }); return { code: 0, output: '[]' } }
  await executeBudgetedDocker(['--context', 'local', 'container', 'inspect', ids[0]], config)
  assert.deepEqual(calls, [{ command: 'docker', args: ['--host', identity.endpoint, 'container', 'inspect', ids[0]] }])
  await assert.rejects(executeBudgetedDocker(['--context', 'other', 'inspect', ids[0]], config))
  for (const option of ['--host=unix:///other', '--context=other', '-Htcp://remote']) {
    await assert.rejects(executeBudgetedDocker(['inspect', ids[0], option], config))
  }
  assert.equal(calls.length, 1)
})

test('context元数据和非白名单写入继续复验daemon身份', async t => {
  const state = fake(); const config = await settings(t, state); const calls = []; const base = config.execute
  config.execute = (command, args, options) => { calls.push(args); return base(command, args, options) }
  await executeBudgetedDocker(['context', 'show'], config)
  assert.deepEqual(calls.at(-1), ['--context', 'local', 'context', 'show'])
  assert.equal(calls.length, 3)
  state.changed = true
  await assert.rejects(executeBudgetedDocker(['container', 'rm', ids[0]], config))
  assert.equal(state.starts, 0)
})

test('同socket换daemon只读仍固定endpoint，下一写入因原daemonID不符拒绝', async t => {
  const state = fake(); state.changed = true; const config = await settings(t, state); const calls = []; const base = config.execute
  config.execute = (command, args, options) => { calls.push(args); return base(command, args, options) }
  const read = await executeBudgetedDocker(['info', '--format', '{{.ID}}'], config)
  assert.equal(read.output, 'other'); assert.deepEqual(calls[0].slice(0, 2), ['--host', identity.endpoint])
  await assert.rejects(executeBudgetedDocker(['container', 'rm', ids[0]], config))
  assert.equal(state.starts, 0)
})

test('只读短context覆写拒绝，不误拦非白名单exec的shell参数', async t => {
  const state = fake(); const config = await settings(t, state)
  for (const option of ['-c', '-cother']) await assert.rejects(executeBudgetedDocker(['inspect', ids[0], option], config))
  await executeBudgetedDocker(['exec', ids[0], 'sh', '-c', 'true'], config)
  assert.equal(state.starts, 1)
})

test('Linux旧宿主PID已换但仍归已计DB容器，不重复计数', async () => {
  const result = await surveyDatabases(linuxSurvey('199 mysqld\n'), {}, 'local', { platform: 'linux', readCgroup: async () => '0::/docker/' + ids[0] })
  assert.equal(result.count, 2)
})

test('Linux消失PID只重采普查三次，持续变化失败关闭', async () => {
  let reads = 0
  await assert.rejects(surveyDatabases(linuxSurvey('199 mysqld\n'), {}, 'local', { platform: 'linux', readCgroup: async () => { reads++; throw Object.assign(new Error('gone'), { code: 'ENOENT' }) } }))
  assert.equal(reads, 3)
})

test('ENOENT后整轮重采成功，稳定外部宿主和非DB容器不被吞掉', async () => {
  let reads = 0
  const options = { platform: 'linux', readCgroup: async () => { if (++reads === 1) throw Object.assign(new Error('gone'), { code: 'ENOENT' }); return '0::/user.slice/host' } }
  assert.equal((await surveyDatabases(linuxSurvey('199 mysqld\n'), {}, 'local', options)).count, 3)
  assert.equal(reads, 2)
  const base = fake(1).execute
  const execute = (command, args, options) => command === 'ps' ? { code: 0, output: '199 mysqld\n' } : args.includes('inspect') ? { code: 0, output: JSON.stringify([{ Id: ids[0], State: { Running: true }, Config: { Image: 'non-db' } }]) } : args.includes('top') ? { code: 0, output: 'PID COMMAND\n200 tail\n' } : base(command, args, options)
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup: async () => '0::/docker/' + ids[0] })).count, 1)
})

test('已存在未知锁不能由随后成功只读普查结算，诊断只含固定字段', async t => {
  const state = fake(); state.unknown = true; const config = await settings(t, state)
  await assert.rejects(executeBudgetedDocker(['run', 'mysql'], config), error => {
    const data = JSON.parse(budgetDiagnosticLine(error))
    assert.equal(data.stage, 'start'); assert.equal(data.extra, 1)
    return error.preserveTemporary === true
  })
  state.unknown = false
  await surveyDatabases(state.execute, {}, 'local')
  await access(join(config.startLock, 'owner')); assert.equal(state.starts, 1)
  const output = budgetDiagnosticLine({ message: 'SECRET', budgetDiagnostic: { code: 'SECRET', stage: 'SECRET', count: 3, extra: 'SECRET', argv: 'SECRET' } })
  assert.deepEqual(JSON.parse(output), { code: 'BUDGET_CHECK_FAILED', stage: 'guard', count: 3 })
  assert.equal(output.includes('SECRET'), false)
})

test('同容器host两进程top一进程仍按max保守计数', async () => {
  const result = await surveyDatabases(linuxSurvey('199 mysqld\n198 mysqld\n'), {}, 'local', { platform: 'linux', readCgroup: async () => '0::/docker/' + ids[0] })
  assert.equal(result.count, 3)
})

test('旧PID消失后重新获取宿主新PID快照，成功且只读', async () => {
  let snapshots = 0; const base = linuxSurvey('')
  const execute = (command, args, options) => command === 'ps' ? { code: 0, output: ++snapshots === 1 ? '199 mysqld\n' : '101 mysqld\n' } : base(command, args, options)
  const readCgroup = async pid => { if (pid === 199) throw Object.assign(new Error('gone'), { code: 'ENOENT' }); return '0::/docker/' + ids[0] }
  assert.equal((await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup })).count, 2)
  assert.equal(snapshots, 2)
})

test('私有sidecar写读双重白名单，原错误及伪造字段不能进入日志', async t => {
  const root = await temporary(t)
  assert.deepEqual(await readBudgetDiagnostics(root), [])
  await recordBudgetDiagnostic(root, { message: 'SECRET', budgetDiagnostic: { code: 'SNAPSHOT_UNSTABLE', stage: 'post_survey', count: 4, extra: 'SECRET' } })
  let lines = await readBudgetDiagnostics(root)
  assert.deepEqual(JSON.parse(lines[0]), { code: 'SNAPSHOT_UNSTABLE', stage: 'post_survey', count: 4 })
  await writeFile(join(root, 'diagnostics.jsonl'), JSON.stringify({ code: 'SECRET', stage: 'SECRET', count: 3, token: 'SECRET' }) + '\n')
  lines = await readBudgetDiagnostics(root)
  assert.equal(lines[0].includes('SECRET'), false); assert.equal(lines[0].includes('\n'), false)
})

test('sidecar拒绝符号链接与非私有权限，不读取任意文件', async t => {
  const root = await temporary(t); const target = join(root, 'unrelated'); await writeFile(target, 'SECRET')
  await symlink(target, join(root, 'diagnostics.jsonl'))
  await assert.rejects(recordBudgetDiagnostic(root, new Error('SECRET')))
  await assert.rejects(readBudgetDiagnostics(root))
  assert.equal(await readFile(target, 'utf8'), 'SECRET')
  const other = await temporary(t); await chmod(other, 0o755)
  await assert.rejects(readBudgetDiagnostics(other))
})

test('真实wrapper子进程诊断落盘失败仅固定码，不泄露异常路径或清未知锁', async t => {
  const { spawnSync } = await import('node:child_process')
  const root = await temporary(t); const guard = join(root, 'guard'); const state = fake()
  const budget = await createMySQLBudget({ directory: guard, execute: state.execute, env: { PATH: '/fake/bin' }, dockerPath: '/fake/SECRET_DOCKER', lockPath: join(root, 'lock') })
  const target = join(root, 'SECRET_FILE'); await writeFile(target, 'SECRET')
  await symlink(target, join(guard, 'diagnostics.jsonl'))
  await mkdir(join(guard, 'starting')); await writeFile(join(guard, 'starting', 'owner'), 'preserve-unknown')
  const result = spawnSync(process.execPath, [join(guard, 'docker'), '--context', 'other', 'inspect'], { encoding: 'utf8' })
  assert.equal(result.status, 1)
  assert.deepEqual(JSON.parse(result.stderr.trim()), { code: 'DIAGNOSTIC_WRITE_FAILED', stage: 'guard' })
  assert.equal(result.stderr.includes(root), false); assert.equal(result.stderr.includes('SECRET'), false)
  assert.equal(await readFile(join(guard, 'starting', 'owner'), 'utf8'), 'preserve-unknown')
  assert.equal(await readFile(target, 'utf8'), 'SECRET')
  assert.equal(typeof budget.release, 'function')
})

test('每轮宿主PID在Docker读取期间退出，先连续采cgroup可消除人为长窗口', async () => {
  let exited = false; let snapshots = 0; const base = linuxSurvey('199 mysqld\n')
  const execute = (command, args, options) => {
    if (command === 'ps') { exited = false; snapshots++ } else exited = true
    return base(command, args, options)
  }
  const readCgroup = async () => { if (exited) throw Object.assign(new Error('gone'), { code: 'ENOENT' }); return '0::/docker/' + ids[0] }
  const result = await surveyDatabases(execute, {}, 'local', { platform: 'linux', readCgroup })
  assert.equal(result.count, 2); assert.equal(snapshots, 1)
})

test('cgroup缺失诊断只分类stat，不改变三次重采失败规则', async () => {
  const makeStat = state => '199 (private name) ' + [state, ...Array(18).fill('0'), '123'].join(' ')
  const cases = [
    ['STAT_LIVE', async () => makeStat('S')],
    ['STAT_EXITED', async () => makeStat('Z')],
    ['STAT_MISSING', async () => { throw Object.assign(new Error('private path'), { code: 'ENOENT' }) }],
    ['STAT_UNREADABLE', async () => { throw Object.assign(new Error('private path'), { code: 'EACCES' }) }],
    ['STAT_UNREADABLE', async () => 'malformed SECRET'],
  ]
  for (const [expected, readStat] of cases) {
    let reads = 0
    await assert.rejects(surveyDatabases(linuxSurvey('199 mysqld\n'), {}, 'local', { platform: 'linux', readStat, readCgroup: async () => { reads++; throw Object.assign(new Error('private'), { code: 'ENOENT' }) } }), error => error.budgetCode === 'SNAPSHOT_UNSTABLE' && error.missingKind === expected)
    assert.equal(reads, 3)
  }
})

test('sidecar诊断保留合法missingKind且删除恶意枚举', async t => {
  const root = await temporary(t)
  for (const missingKind of ['STAT_LIVE', 'STAT_EXITED', 'STAT_MISSING', 'STAT_UNREADABLE', 'SECRET/path']) await recordBudgetDiagnostic(root, { budgetDiagnostic: { code: 'SNAPSHOT_UNSTABLE', stage: 'post_survey', missingKind } })
  const lines = (await readBudgetDiagnostics(root)).map(JSON.parse)
  assert.equal(lines[0].missingKind, 'STAT_LIVE'); assert.equal(lines[3].missingKind, 'STAT_UNREADABLE')
  assert.equal(lines[4].missingKind, undefined)
})
