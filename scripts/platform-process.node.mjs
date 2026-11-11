/** 只用可控假进程验证收回顺序，不启动数据库或共享服务。 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { stopPlatformProcess, startOwnedPlatform, isProcessExitUnconfirmed } from './platform-process.mjs'
/** 模拟明确退出或无法退出的自有子进程。 */
function child(exits) {
  const value = new EventEmitter(); value.exitCode = null; value.signalCode = null; value.pid = 123; value.signals = []
  value.kill = signal => { value.signals.push(signal); if (exits(signal)) setTimeout(() => { value.signalCode = signal; value.emit('exit', signal === 'SIGTERM' ? 0 : null, signal); value.emit('close') }, 2); return true }
  return value
}
test('正常退出等待确认，超时强杀仍等待退出后失败', async () => {
  const graceful = child(() => true)
  await stopPlatformProcess(graceful, { timeoutMs: 20, killTimeoutMs: 20 })
  assert.deepEqual(graceful.signals, ['SIGTERM'])
  const forced = child(signal => signal === 'SIGKILL')
  await assert.rejects(stopPlatformProcess(forced, { timeoutMs: 5, killTimeoutMs: 20 }), error => !isProcessExitUnconfirmed(error) && /强制/.test(error.message))
  assert.equal(forced.signalCode, 'SIGKILL')
})
test('无法确认强杀退出必须标记保留现场且移除所有等待监听器', async () => {
  const blocked = child(() => false)
  await assert.rejects(stopPlatformProcess(blocked, { timeoutMs: 5, killTimeoutMs: 5 }), isProcessExitUnconfirmed)
  assert.deepEqual(blocked.signals, ['SIGTERM', 'SIGKILL'])
  assert.equal(blocked.listenerCount('exit'), 0)
})
test('启动失败必定收回，双失败保留原错误及未知退出', async () => {
  const original = new Error('启动失败'); let stopped = 0
  await assert.rejects(startOwnedPlatform(async () => { throw original }, async () => { stopped++ }), error => error === original)
  assert.equal(stopped, 1)
  const blocked = child(() => false)
  await assert.rejects(startOwnedPlatform(async () => { throw original }, () => stopPlatformProcess(blocked, { timeoutMs: 5, killTimeoutMs: 5 })), error => error instanceof AggregateError && error.errors[0] === original && isProcessExitUnconfirmed(error))
})

test('未启动或已退出不会重复发信号，已知非正常退出保持失败', async () => {
  await stopPlatformProcess(undefined)
  await stopPlatformProcess({ pid: undefined })
  const stopped = child(() => false); stopped.exitCode = 0
  await stopPlatformProcess(stopped)
  assert.deepEqual(stopped.signals, [])
  stopped.exitCode = 2
  await assert.rejects(stopPlatformProcess(stopped), /非正常/)
  assert.equal(isProcessExitUnconfirmed(new Error('普通故障')), false)
})
test('发送信号失败必须保留未确认状态且不遗留监听器', async () => {
  const broken = child(() => false)
  broken.kill = () => { throw new Error('信号发送失败') }
  await assert.rejects(stopPlatformProcess(broken), error => isProcessExitUnconfirmed(error) && error.errors[0].message === '信号发送失败')
  assert.equal(broken.listenerCount('exit'), 0)
})
test('成功启动不触发清理，实际非正常退出不会报告成功', async () => {
  let stopped = false
  assert.equal(await startOwnedPlatform(async () => 'ready', async () => { stopped = true }), 'ready')
  assert.equal(stopped, false)
  const failed = child(() => false)
  failed.kill = () => { setTimeout(() => { failed.exitCode = 2; failed.emit('exit', 2, null) }, 1); return true }
  await assert.rejects(stopPlatformProcess(failed, { timeoutMs: 20 }), /非正常/)
})

test('已确认强杀仍失败但允许清理，未知退出不得清理', async () => {
  const { finishOwnedPlatform } = await import('./platform-process.mjs')
  for (const confirmed of [true, false]) {
    const process = child(signal => confirmed && signal === 'SIGKILL')
    const original = new Error('验收原错误'); let cleaned = false
    await assert.rejects(finishOwnedPlatform([original], () => stopPlatformProcess(process, { timeoutMs: 5, killTimeoutMs: 20 }), async () => { cleaned = true }), error => error instanceof AggregateError && error.errors[0] === original && isProcessExitUnconfirmed(error) === !confirmed)
    assert.equal(cleaned, confirmed)
  }
})
test('已确认退出的清理错误与原错误共同保留', async () => {
  const { finishOwnedPlatform } = await import('./platform-process.mjs')
  const original = new Error('验收失败'); const cleanup = new Error('目录清理失败')
  await assert.rejects(finishOwnedPlatform([original], async () => {}, async () => { throw cleanup }), error => error instanceof AggregateError && error.errors[0] === original && error.errors[1] === cleanup)
  await finishOwnedPlatform([], async () => {}, async () => {})
})
