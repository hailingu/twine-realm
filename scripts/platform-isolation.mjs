/** 全量测试精确分组及逐例schema隔离，不引入本地/远端分阶段。 */
import { readFile } from 'node:fs/promises'
import { randomBytes } from 'node:crypto'
import { join } from 'node:path'
import { requiresPreservation } from './runtime-check.mjs'

/** 列举的完整Go集合必须与审核清单一致，新增测试先明确资源边界。 */
export async function discoverPlatformTests({ root, execute, env }) {
  const inventory = JSON.parse(await readFile(join(root, 'scripts/platform-test-inventory.json'), 'utf8'))
  const listed = await execute('go', ['test', '-json', '-tags=integration', '-list=.', './...'], { cwd: join(root, 'server'), env, timeoutMs: 600000 })
  if (listed.code !== 0) throw new Error('完整Go测试列举失败')
  const actual = listed.output.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)).filter(row => /^Test\w+$/.test(row.Output?.trim() ?? '')).map(row => `${row.Package}:${row.Output.trim()}`)
  const tests = inventory.filter(row => row.file.endsWith('.go') && !row.file.includes('/_template/'))
  const expected = tests.map(row => `github.com/hailingu/twine-realm/server/${row.file.slice(7, row.file.lastIndexOf('/'))}:${row.name}`)
  sameSet(actual, expected)
  return { tests, browser: inventory.filter(row => row.file.endsWith('.spec.ts')) }
}

/** 不允许重复、遗漏或未审核的测试。 */
function sameSet(actual, expected) {
  const sorted = values => [...values].sort((a, b) => a.localeCompare(b))
  if (new Set(actual).size !== actual.length || JSON.stringify(sorted(actual)) !== JSON.stringify(sorted(expected))) throw new Error('实际测试与审核清单不一致')
}

/** 普通测试按包执行，真实物理及旧schema用例每次只选一个顶层测试。 */
export function goGroups(tests) {
  const groups = new Map()
  const isolated = []
  for (const test of tests) {
    const pkg = './' + test.file.slice(7, test.file.lastIndexOf('/'))
    if (test.isolated) isolated.push({ pkg, names: [test.name] })
    else {
      if (!groups.has(pkg)) groups.set(pkg, [])
      groups.get(pkg).push(test.name)
    }
  }
  return [...groups].map(([pkg, names]) => ({ pkg, names })).concat(isolated)
}

/** 每组均独占schema；进程退出后先清资源，再撤销schema，异常不推进下一组。 */
export async function isolatedCase({ env, schemaCommand, cleanup, action }) {
  const schema = `twine_case_${randomBytes(12).toString('hex')}`
  const original = { database: env.TWINE_DATABASE_DSN, test: env.TWINE_TEST_DATABASE_DSN }
  try {
    await schemaCommand(`CREATE DATABASE ${schema};`)
    await schemaCommand(`GRANT ALL ON ${schema}.* TO 'twine'@'%';`)
  } catch (error) {
    // 创建结果不明不能按名称猜测删除，保留随机schema身份供本轮核实。
    error.preserveTemporary = true
    error.message = `隔离schema准备未确认（${schema}）：${error.message}`
    throw error
  }
  env.TWINE_DATABASE_DSN = original.database.replace(/\/twine_test$/, `/${schema}`)
  env.TWINE_TEST_DATABASE_DSN = original.test.replace(/\/twine_test$/, `/${schema}`)
  const failures = []
  try { await action() } catch (error) { failures.push(error) }
  if (!failures.some(error => requiresPreservation(error))) try { await cleanup() } catch (error) { error.preserveTemporary = true; failures.push(error) }
  if (!failures.some(error => requiresPreservation(error))) {
    try { await schemaCommand(`REVOKE ALL ON ${schema}.* FROM 'twine'@'%'; DROP DATABASE ${schema};`) } catch (error) { error.preserveTemporary = true; failures.push(error) }
  }
  env.TWINE_DATABASE_DSN = original.database
  env.TWINE_TEST_DATABASE_DSN = original.test
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}

/** 列举所有浏览器场景，逐file:line运行，拒绝skip/only及未分类新用例。 */
export async function discoverBrowserTests({ root, execute, env, expected }) {
  const result = await execute(process.execPath, ['node_modules/@playwright/test/cli.js', 'test', '--list', '--reporter=json', '--forbid-only'], { cwd: join(root, 'web'), env, timeoutMs: 600000 })
  if (result.code !== 0) throw new Error('浏览器完整测试列举失败')
  const found = []
  const visit = suite => {
    for (const spec of suite.specs ?? []) {
      if (spec.tests?.some(test => test.expectedStatus !== 'passed')) throw new Error('浏览器用例包含跳过声明')
      found.push({ file: `web/e2e/${spec.file.replace(/^e2e\//, '')}`, name: spec.title, line: spec.line })
    }
    for (const child of suite.suites ?? []) visit(child)
  }
  for (const suite of JSON.parse(result.output).suites ?? []) visit(suite)
  sameSet(found.map(row => `${row.file}:${row.name}`), expected.map(row => `${row.file}:${row.name}`))
  if (found.some(row => !Number.isInteger(row.line) || row.line < 1)) throw new Error('浏览器精确行号无效')
  return found
}

/** 实际顶层pass必须覆盖精确选择，跳过/失败不能以进程退出零替代。 */
export function verifyGoCompletion(output, names) {
  const events = output.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)).filter(row => row.Test)
  if (events.some(row => ['skip', 'fail'].includes(row.Action))) throw new Error('Go用例跳过或失败')
  sameSet(events.filter(row => row.Action === 'pass' && !row.Test.includes('/')).map(row => row.Test), names)
}

/** @param output 本次Go JSON事件 @param names 本组精确测试名 @returns 当前测试真实输出的夹具缓存证据。 */
export function fixtureCacheEvidence(output, names) {
  const entries = []
  const pattern = /\[FIXTURE_BUILD_CACHE\] (cold|hit) operation ([0-9a-f-]{36}) source (sha256:[0-9a-f]{64}) binary (sha256:[0-9a-f]{64}) process ([1-9]\d*)\s*$/
  for (const row of output.trim().split('\n').filter(Boolean).map(line => JSON.parse(line))) {
    if (!row.Output?.includes('[FIXTURE_BUILD_CACHE]')) continue
    const match = row.Output.match(pattern)
    if (row.Action !== 'output' || !names.includes(row.Test) || !match || !Number.isSafeInteger(Number(match[5]))) throw new Error('夹具缓存证据格式或测试归属不明确')
    entries.push({ mode: match[1], operation: match[2], source: match[3], binary: match[4], process: Number(match[5]) })
  }
  return entries
}

/** @param entries 同轮所有夹具证据 @returns 有真实冷构建和后续命中的审计摘要；不能用全部miss冒充缓存提速。 */
export function verifyFixtureCacheEvidence(entries) {
  const operations = new Set(); const contents = new Map(); let cold = 0; let hits = 0; let crossProcessHits = 0
  for (const entry of entries) {
    if (operations.has(entry.operation)) throw new Error('夹具缓存重复使用操作身份')
    operations.add(entry.operation)
    const key = `${entry.source}/${entry.binary}`
    if (entry.mode === 'cold') { cold++; contents.set(key, new Set([...(contents.get(key) ?? []), entry.process])) }
    else if (entry.mode === 'hit' && contents.has(key)) {
      hits++
      if ([...contents.get(key)].some(process => process !== entry.process)) crossProcessHits++
    }
    else throw new Error('夹具缓存命中缺少本轮较早的真实构建证明')
  }
  if (!cold || !hits || !crossProcessHits) throw new Error('夹具缓存未证明本轮冷构建和跨进程命中')
  return { cold, hits, contents: contents.size, crossProcessHits }
}
