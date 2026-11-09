/** 三实例编排的纯函数与失败边界，不启动数据库。 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { goGroups, isolatedCase, verifyGoCompletion } from './platform-isolation.mjs'

test('真实物理逐顶层用例且跨包完整，普通用例按包不遗失', () => {
  const entries = ['TestA', 'TestB', 'TestRealOne', 'TestRealTwo'].map(name => ({ file: 'server/internal/a/a_test.go', name, isolated: name.startsWith('TestReal') }))
  assert.deepEqual(goGroups(entries).map(row => row.names), [['TestA', 'TestB'], ['TestRealOne'], ['TestRealTwo']])
})
test('进程退出后先清资源再删除独立schema，原DSN恢复', async () => {
  const env = { TWINE_DATABASE_DSN: 'x/twine_test', TWINE_TEST_DATABASE_DSN: 'x/twine_test' }
  const events = []
  await isolatedCase({ env, schemaCommand: async sql => events.push(sql), action: async () => { assert.match(env.TWINE_DATABASE_DSN, /twine_case_/); events.push('exit') }, cleanup: async () => events.push('clean') })
  assert.equal(events[2], 'exit'); assert.equal(events[3], 'clean'); assert.match(events[4], /^REVOKE.*DROP DATABASE/)
  assert.equal(env.TWINE_DATABASE_DSN, 'x/twine_test')
})
test('资源未确认清理禁止删除schema和继续，原失败保留', async () => {
  const events = []; const env = { TWINE_DATABASE_DSN: 'x/twine_test', TWINE_TEST_DATABASE_DSN: 'x/twine_test' }
  await assert.rejects(isolatedCase({ env, schemaCommand: async sql => events.push(sql), action: async () => { throw new Error('测试失败') }, cleanup: async () => { throw Object.assign(new Error('归属不明'), { preserveTemporary: true }) } }), /测试失败/)
  assert.equal(events.length, 2)
})

test('嵌套未知错误保留schema且不尝试资源删除', async () => {
  let cleaned = false; const statements = []
  const unknown = Object.assign(new Error('未知'), { preserveTemporary: true })
  await assert.rejects(isolatedCase({ env: { TWINE_DATABASE_DSN: 'x/twine_test', TWINE_TEST_DATABASE_DSN: 'x/twine_test' }, schemaCommand: async sql => statements.push(sql), cleanup: async () => { cleaned = true }, action: async () => { throw new AggregateError([unknown], '外层') } }))
  assert.equal(cleaned, false); assert.equal(statements.length, 2)
})

test('实际Go完成集合拒绝跳过和遗漏，不依赖零退出码', () => {
  assert.throws(() => verifyGoCompletion(JSON.stringify({ Test: 'TestA', Action: 'skip' }), ['TestA']), /跳过/)
  assert.throws(() => verifyGoCompletion(JSON.stringify({ Test: 'TestA', Action: 'pass' }), ['TestA', 'TestB']), /不一致/)
  verifyGoCompletion(JSON.stringify({ Test: 'TestA', Action: 'pass' }), ['TestA'])
})

test('撤权或删schema失败保留现场并保留原测试错误', async () => {
  const env = { TWINE_DATABASE_DSN: 'x/twine_test', TWINE_TEST_DATABASE_DSN: 'x/twine_test' }
  await assert.rejects(isolatedCase({ env, action: async () => { throw new Error('原始测试失败') }, cleanup: async () => {}, schemaCommand: async sql => { if (sql.startsWith('REVOKE')) throw new Error('schema清理失败') } }), error => error instanceof AggregateError && error.errors[0].message === '原始测试失败' && error.errors[1].preserveTemporary === true)
  assert.equal(env.TWINE_DATABASE_DSN, 'x/twine_test')
})
