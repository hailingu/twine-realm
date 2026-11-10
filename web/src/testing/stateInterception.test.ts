/** 真实信封的测试运输边界须读完、验证归属并交付，旧游标不能充当UI应用证据。 */
import { expect, test, vi } from 'vitest'
import { deliverState, appliedCursor } from '../../e2e/stateInterception'
import { stateSnapshot } from './stateFixtures'

/** 构造完整v6读取信封，保留严格契约验证。 */
function envelope() {
  const state = stateSnapshot('2')
  return { schemaVersion: 1, state, lifecycle: { schemaVersion: 1, projectId: state.project.id, generation: '0', target: null, operation: null, facts: null } }
}
test('响应头到达但body未结束不交付，fulfill完成后才返回', async () => {
  let release!: (value: unknown) => void
  const body = new Promise<unknown>(resolve => { release = resolve })
  let finish!: () => void
  const fulfillment = new Promise<void>(resolve => { finish = resolve })
  const fulfill = vi.fn(() => fulfillment); let done = false
  const pending = deliverState({ status: () => 200, json: () => body }, envelope().state.project.id, fulfill).then(() => { done = true })
  await Promise.resolve(); expect(fulfill).not.toHaveBeenCalled()
  release(envelope()); await vi.waitFor(() => expect(fulfill).toHaveBeenCalledOnce()); expect(done).toBe(false)
  finish(); await pending; expect(done).toBe(true)
})
test('解析、项目归属与交付失败均传播', async () => {
  const fulfill = vi.fn(() => Promise.resolve())
  await expect(deliverState({ status: () => 200, json: () => Promise.resolve({}) }, 'other', fulfill)).rejects.toThrow()
  await expect(deliverState({ status: () => 200, json: () => Promise.resolve(envelope()) }, 'other', fulfill)).rejects.toThrow()
  expect(fulfill).not.toHaveBeenCalled()
  const error = new Error('交付失败')
  await expect(deliverState({ status: () => 200, json: () => Promise.resolve(envelope()) }, envelope().state.project.id, () => Promise.reject(error))).rejects.toBe(error)
})
test('旧DOM或未推进快照不能通过，新快照游标精确匹配才通过', () => {
  expect(appliedCursor('1', '2', '1')).toBe(false)
  expect(appliedCursor('1', '1', '1')).toBe(false)
  expect(appliedCursor('1', '2', '3')).toBe(false)
  expect(appliedCursor('1', '2', '2')).toBe(true)
})

test('清理总是执行且双失败保留主错误，单清理失败仍失败', async () => {
  const { withStateCleanup } = await import('../../e2e/stateInterception')
  const primary = new Error('读取失败'); const cleanup = new Error('abort失败')
  const clean = vi.fn(() => Promise.reject(cleanup))
  await expect(withStateCleanup(() => Promise.reject(primary), clean)).rejects.toMatchObject({ errors: [primary, cleanup] })
  expect(clean).toHaveBeenCalledOnce()
  await expect(withStateCleanup(() => Promise.resolve(), clean)).rejects.toBe(cleanup)
  await expect(withStateCleanup(() => Promise.reject(primary), () => Promise.resolve())).rejects.toBe(primary)
})

test('已fulfill后诊断失败不abort，未交付失败只abort一次', async () => {
  const { ownStateDelivery } = await import('../../e2e/stateInterception')
  const error = new Error('诊断失败'); const abort = vi.fn(() => Promise.resolve())
  await expect(ownStateDelivery(mark => { mark(); return Promise.resolve('2') }, abort, () => Promise.reject(error))).rejects.toBe(error)
  expect(abort).not.toHaveBeenCalled()
  await expect(ownStateDelivery(() => Promise.reject(error), abort)).rejects.toBe(error)
  expect(abort).toHaveBeenCalledOnce()
})
test('清理永不返回也有界失败，迟到拒绝不产生未处理异常', async () => {
  const { boundedStateTask } = await import('../../e2e/stateInterception')
  vi.useFakeTimers()
  try {
    let reject!: (error: Error) => void
    const pending = new Promise<void>((_resolve, fail) => { reject = fail })
    const failed = expect(boundedStateTask(pending)).rejects.toThrow('既有期限')
    await vi.advanceTimersByTimeAsync(8000); await failed
    reject(new Error('迟到清理失败')); await Promise.resolve()
  } finally { vi.useRealTimers() }
})
