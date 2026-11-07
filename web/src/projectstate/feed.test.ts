/** 受控网络时序证明快照领先、旧流、缺口和退避，不模拟真实资源运行。 */
import { afterEach, expect, it, vi } from 'vitest'
import { ProjectFeed, type FeedValue } from './feed'
import { StateReadError } from './api'
import { controlledStateAPI, stateEvent, stateSnapshot, stateStream } from '../testing/stateFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'
import { RequestError } from '../project/api'

afterEach(() => { vi.useRealTimers() })

/** @param cursor 控制游标 @returns 明确追平的独立控制消息。 */
function caught(cursor: string) { return { kind: 'caughtUp' as const, value: { schemaVersion: 1 as const, projectId: INITIAL_PROJECT.id, cursor } } }

it('HTTP快照领先不跳过重放；去重、追平、断线原游标和旧连接围栏共同成立', async () => {
  vi.useFakeTimers(); const { api, streams } = controlledStateAPI(); api.snapshot.mockResolvedValue(stateSnapshot('5'))
  const values: FeedValue[] = []; const controller = new AbortController(); const task = new ProjectFeed(api, INITIAL_PROJECT.id, controller.signal, value => { values.push(value) }).run()
  try {
    await vi.advanceTimersByTimeAsync(0); const first = stateStream(streams)
    api.snapshot.mockResolvedValue(stateSnapshot('9', '2'))
    first.emit({ kind: 'state', value: stateEvent('6') }); first.emit({ kind: 'state', value: stateEvent('7') }); first.emit({ kind: 'state', value: stateEvent('6') }); first.emit(caught('7'))
    await vi.advanceTimersByTimeAsync(0)
    expect(values.at(-1)).toMatchObject({ phase: 'live', snapshot: { cursor: '9', project: { revision: '2' } } })
    first.fail(new StateReadError('网络断开')); await vi.advanceTimersByTimeAsync(0)
    expect(values.at(-1)?.phase).toBe('disconnected')
    await vi.advanceTimersByTimeAsync(2000); const second = stateStream(streams, 1)
    expect(second.cursor).toBe('7')
    first.emit({ kind: 'state', value: stateEvent('8') }); first.emit(caught('8'))
    second.emit({ kind: 'state', value: stateEvent('8') }); second.emit({ kind: 'state', value: stateEvent('9') }); second.emit(caught('9'))
    await vi.advanceTimersByTimeAsync(0)
    expect(values.at(-1)?.phase).toBe('live'); expect(api.snapshot).toHaveBeenCalledTimes(3)
  } finally { controller.abort(); await task }
  expect(streams.every(stream => stream.signal.aborted)).toBe(true)
})

it.each(['gap', 'expired', 'wrongCaughtUp'] as const)('不连续或失效的%s在退避后完整恢复，不能直接标记实时', async reason => {
  vi.useFakeTimers(); const { api, streams } = controlledStateAPI(); const values: FeedValue[] = []; const controller = new AbortController()
  const task = new ProjectFeed(api, INITIAL_PROJECT.id, controller.signal, value => { values.push(value) }).run()
  try {
    await vi.advanceTimersByTimeAsync(0); const first = stateStream(streams)
    if (reason === 'gap') first.emit({ kind: 'state', value: stateEvent('3') })
    else if (reason === 'wrongCaughtUp') first.emit(caught('2'))
    else first.emit({ kind: 'resync', value: { schemaVersion: 1, projectId: INITIAL_PROJECT.id, code: 'STATE_CURSOR_EXPIRED' } })
    await vi.advanceTimersByTimeAsync(0); expect(values.at(-1)?.phase).toBe('disconnected')
    api.snapshot.mockResolvedValue(stateSnapshot('5', '2'))
    await vi.advanceTimersByTimeAsync(2000); expect(stateStream(streams, 1).cursor).toBe('5')
    stateStream(streams, 1).emit(caught('5')); expect(values.at(-1)?.phase).toBe('live')
  } finally { controller.abort(); await task }
})

it('遵循Retry-After且取消等待释放计时器，终止错误不会偷偷重连', async () => {
  vi.useFakeTimers(); const { api, streams } = controlledStateAPI(); const controller = new AbortController(); const values: FeedValue[] = []
  const task = new ProjectFeed(api, INITIAL_PROJECT.id, controller.signal, value => { values.push(value) }).run()
  try {
    await vi.advanceTimersByTimeAsync(0); stateStream(streams).fail(new StateReadError('已限流', false, 5000))
    await vi.advanceTimersByTimeAsync(4999); expect(api.events).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1); expect(api.events).toHaveBeenCalledTimes(2)
    stateStream(streams, 1).fail(new StateReadError('需手动核实', false, null, true)); await vi.advanceTimersByTimeAsync(0)
    expect(values.at(-1)?.phase).toBe('failed')
    await vi.advanceTimersByTimeAsync(60000); expect(api.events).toHaveBeenCalledTimes(2)
  } finally { controller.abort(); await task }
})

it('读取失败属于当前重连轮次，旧流错误不能覆盖新的Retry-After', async () => {
  vi.useFakeTimers(); const { api, streams } = controlledStateAPI(); const values: FeedValue[] = []; const controller = new AbortController()
  api.snapshot.mockResolvedValueOnce(stateSnapshot()).mockRejectedValueOnce(new RequestError({ code: 'STATE_PROTOCOL_INVALID', message: '旧流读取无效' }, 0)).mockRejectedValueOnce(new StateReadError('新读取等待服务', false, 5000))
  const task = new ProjectFeed(api, INITIAL_PROJECT.id, controller.signal, value => { values.push(value) }).run()
  try {
    await vi.advanceTimersByTimeAsync(0); stateStream(streams).emit({ kind: 'state', value: stateEvent('2') })
    await vi.advanceTimersByTimeAsync(0); expect(values.at(-1)?.message).toContain('旧流')
    await vi.advanceTimersByTimeAsync(2000); expect(values.at(-1)?.message).toBe('新读取等待服务')
    await vi.advanceTimersByTimeAsync(4999); expect(api.snapshot).toHaveBeenCalledTimes(3)
  } finally { controller.abort(); await task }
})
