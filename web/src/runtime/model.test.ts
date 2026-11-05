/** 时序测试保护原请求与三套独立版本，避免迟到响应或陈旧事实回退投影。 */
import { expect, it } from 'vitest'
import { EMPTY_RUNTIME, READY_RUNTIME, RUNTIME_CAPABILITY, RUNTIME_COMMAND, RUNTIME_RECEIPT } from '../testing/runtimeFixtures'
import { MYSQL_NODE } from '../testing/projectFixtures'
import { initialRuntime, runtimeLabel, runtimeReducer, submissionPending } from './model'

it('原键未知期间拒绝另一请求，迟到完成不能解除保护，回执须跨过快照屏障', () => {
  let state = runtimeReducer(initialRuntime(), { type: 'snapshot', value: EMPTY_RUNTIME })
  state = runtimeReducer(state, { type: 'sending', command: RUNTIME_COMMAND })
  const pending = state
  state = runtimeReducer(state, { type: 'sending', command: { ...RUNTIME_COMMAND, mutationId: 'other' } })
  expect(state).toBe(pending)
  state = runtimeReducer(state, { type: 'unknown', id: 'other', message: '迟到' })
  expect(state).toBe(pending)
  state = runtimeReducer(state, { type: 'unknown', id: RUNTIME_COMMAND.mutationId, message: '响应丢失' })
  state = runtimeReducer(state, { type: 'verifying', id: RUNTIME_COMMAND.mutationId })
  expect(state.submission).toMatchObject({ kind: 'unknown', verifying: true, command: RUNTIME_COMMAND })
  expect(submissionPending(state)).toBe(true)
  state = runtimeReducer(state, { type: 'accepted', id: RUNTIME_COMMAND.mutationId, receipt: RUNTIME_RECEIPT })
  expect(state.submission.kind).toBe('accepted')
  state = runtimeReducer(state, { type: 'snapshot', value: EMPTY_RUNTIME })
  expect(state.submission.kind).toBe('accepted')
  state = runtimeReducer(state, { type: 'snapshot', value: READY_RUNTIME })
  expect(state.submission.kind).toBe('idle')
  expect(state.lastReceipt).toBe(RUNTIME_RECEIPT)
})

it('代次、事件和采集时间分别单调，读取失败保留最后事实，恢复后清除读取错误', () => {
  let state = runtimeReducer(initialRuntime(), { type: 'snapshot', value: READY_RUNTIME })
  for (const value of [EMPTY_RUNTIME, { ...READY_RUNTIME, sequence: '15' }, { ...READY_RUNTIME, lastAttemptAt: '2026-11-05T08:59:59Z' }]) expect(runtimeReducer(state, { type: 'snapshot', value })).toBe(state)
  state = runtimeReducer(state, { type: 'readFailed', message: '控制面失联' })
  expect(state.snapshot).toBe(READY_RUNTIME)
  expect(state.readError).toBe('控制面失联')
  state = runtimeReducer(state, { type: 'snapshot', value: { ...READY_RUNTIME, sequence: '17' } })
  expect(state.readError).toBeNull()
  state = runtimeReducer(state, { type: 'clock', now: 100 })
  state = runtimeReducer(state, { type: 'clock', now: 99 })
  expect(state.now).toBe(100)
})

it('已知拒绝解除提交保护，先到的新快照允许后到原回执直接确认', () => {
  let state = runtimeReducer(initialRuntime(), { type: 'sending', command: RUNTIME_COMMAND })
  state = runtimeReducer(state, { type: 'rejected', id: RUNTIME_COMMAND.mutationId, message: '配置冲突' })
  expect(submissionPending(state)).toBe(false)
  expect(runtimeReducer(state, { type: 'accepted', id: RUNTIME_COMMAND.mutationId, receipt: RUNTIME_RECEIPT })).toBe(state)
  state = runtimeReducer(state, { type: 'snapshot', value: READY_RUNTIME })
  state = runtimeReducer(state, { type: 'sending', command: RUNTIME_COMMAND })
  state = runtimeReducer(state, { type: 'verifying', id: RUNTIME_COMMAND.mutationId })
  state = runtimeReducer(state, { type: 'accepted', id: RUNTIME_COMMAND.mutationId, receipt: RUNTIME_RECEIPT })
  expect(state.submission.kind).toBe('idle')
})

it('能力、未知、陈旧和资源漂移均不得显示实时健康；认证就绪与仅进程运行有区别', () => {
  const now = Date.parse('2026-11-05T09:00:01Z')
  let state = initialRuntime()
  expect(runtimeLabel(state, MYSQL_NODE.id, now).tone).toBe('unknown')
  state = runtimeReducer(state, { type: 'capability', value: { ...RUNTIME_CAPABILITY, enabled: false } })
  expect(runtimeLabel(state, MYSQL_NODE.id, now).text).toContain('未启用')
  state = runtimeReducer(state, { type: 'capability', value: RUNTIME_CAPABILITY })
  expect(runtimeLabel(state, MYSQL_NODE.id, now).tone).toBe('unknown')
  state = runtimeReducer(state, { type: 'snapshot', value: EMPTY_RUNTIME })
  expect(runtimeLabel(state, MYSQL_NODE.id, now).text).toContain('尚未登记')
  state = runtimeReducer(state, { type: 'snapshot', value: READY_RUNTIME })
  expect(runtimeLabel(state, MYSQL_NODE.id, now).tone).toBe('ready')
  expect(runtimeLabel(state, MYSQL_NODE.id, now + 21000).text).toContain('过期')
  expect(runtimeLabel({ ...state, readError: '未知' }, MYSQL_NODE.id, now).tone).toBe('unknown')
  for (const freshness of ['stale', 'unknown'] as const) expect(runtimeLabel({ ...state, snapshot: { ...READY_RUNTIME, freshness } }, MYSQL_NODE.id, now).tone).toBe('unknown')
  expect(runtimeLabel({ ...state, snapshot: { ...READY_RUNTIME, observationCode: 'RUNTIME_DRIFT' } }, MYSQL_NODE.id, now).tone).toBe('error')
  if (!READY_RUNTIME.observation) throw new Error('缺认证事实夹具')
  for (const [change, tone] of [[{ ready: false }, 'pending'], [{ running: false, ready: false, state: 'stopped' }, 'stopped'], [{ running: false, ready: false, state: 'not_created' }, 'stopped']] as const) expect(runtimeLabel({ ...state, snapshot: { ...READY_RUNTIME, observation: { ...READY_RUNTIME.observation, ...change } } }, MYSQL_NODE.id, now).tone).toBe(tone)
})
