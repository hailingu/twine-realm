/** 网络替身只验证真实客户端分帧、校验、请求头与边界错误。 */
import { afterEach, expect, it, vi } from 'vitest'
import { STATE_API, StateReadError, retryAfter } from './api'
import { stateEvent, stateSnapshot } from '../testing/stateFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'
import type { StateMessage } from './model'

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

/** @param text 原始传输 @returns 每次独立的可消费响应，不重复复用已读取的Response。 */
function streamResponse(text: string): Response { return new Response(text, { headers: { 'Content-Type': 'text/event-stream; charset=utf-8' } }) }

it('快照完整校验，错误归属、空体与超过64MiB都会失败而非部分成功', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(stateSnapshot())))); vi.stubGlobal('fetch', fetch)
  const signal = new AbortController().signal
  expect((await STATE_API.snapshot(INITIAL_PROJECT.id, signal)).cursor).toBe('1')
  expect((await STATE_API.snapshot(INITIAL_PROJECT.id, signal)).project.id).toBe(INITIAL_PROJECT.id)
  fetch.mockResolvedValueOnce(new Response('{}'))
  await expect(STATE_API.snapshot(INITIAL_PROJECT.id, signal)).rejects.toThrow()
  fetch.mockResolvedValueOnce(new Response(null)); await expect(STATE_API.snapshot(INITIAL_PROJECT.id, signal)).rejects.toThrow()
  const chunk = new Uint8Array(1024 * 1024); let count = 0; const cancel = vi.fn()
  fetch.mockResolvedValueOnce(new Response(new ReadableStream<Uint8Array>({ pull(controller) { count++; controller.enqueue(chunk) }, cancel })))
  await expect(STATE_API.snapshot(INITIAL_PROJECT.id, signal)).rejects.toThrow(); expect(cancel).toHaveBeenCalled(); expect(count).toBeLessThanOrEqual(66)
})

it('使用Last-Event-ID且不把控制帧或EOF半帧当编辑确认，正常EOF按原游标重连', async () => {
  const id = INITIAL_PROJECT.id; const values: StateMessage[] = []
  const wire = `event: state\nid: 2\ndata: ${JSON.stringify(stateEvent('2'))}\n\nevent: caught_up\ndata: ${JSON.stringify({ schemaVersion: 1, projectId: id, cursor: '2' })}\n\nevent: state\nid: 3\ndata: {`
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValueOnce(streamResponse(wire)); vi.stubGlobal('fetch', fetch)
  await expect(STATE_API.events(id, '1', new AbortController().signal, value => { values.push(value) })).rejects.toMatchObject({ resync: false })
  expect(values.map(value => value.kind)).toEqual(['state', 'caughtUp'])
  expect(fetch.mock.calls[0]?.[0]).toBe(`/api/v5/projects/${id}/state/events`)
  expect(fetch.mock.calls[0]?.[1]?.headers).toEqual({ Accept: 'text/event-stream', 'Last-Event-ID': '1' })
})

it('非法协议和超大帧要求完整恢复，普通网络失败保留重放游标', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(); vi.stubGlobal('fetch', fetch)
  for (const text of ['event: state\ndata: {}\n\n', 'event: future\ndata: {}\n\n', 'data: '+ 'x'.repeat(4096) + '\n\n']) {
    fetch.mockResolvedValueOnce(streamResponse(text))
    await expect(STATE_API.events(INITIAL_PROJECT.id, '1', new AbortController().signal, () => { throw new Error('不应交付') })).rejects.toMatchObject({ resync: true })
  }
  fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
  await expect(STATE_API.events(INITIAL_PROJECT.id, '1', new AbortController().signal, () => { throw new Error('不应交付') })).rejects.toMatchObject({ resync: false })
  fetch.mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'STATE_CURSOR_EXPIRED', message: '窗口过期' } }), { status: 409, headers: { 'Retry-After': '5' } }))
  await expect(STATE_API.events(INITIAL_PROJECT.id, '1', new AbortController().signal, () => { throw new Error('不应交付') })).rejects.toMatchObject({ resync: true, retryMs: 5000 })
})

it('45秒无数据取消自己的读取，Retry-After支持秒数与HTTP日期且不静默截断超大等待', async () => {
  vi.useFakeTimers(); const owner = new AbortController()
  vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockImplementation((_url, options) => Promise.resolve(new Response(new ReadableStream<Uint8Array>({ start(controller) { options?.signal?.addEventListener('abort', () => { controller.error(new DOMException('读取终止', 'AbortError')) }, { once: true }) } }), { headers: { 'Content-Type': 'text/event-stream' } }))))
  const pending = expect(STATE_API.events(INITIAL_PROJECT.id, '1', owner.signal, () => { throw new Error('不应交付') })).rejects.toBeInstanceOf(StateReadError)
  await vi.advanceTimersByTimeAsync(45000); await pending
  expect(owner.signal.aborted).toBe(false); expect(vi.getTimerCount()).toBe(0)
  expect(retryAfter('2')).toBe(2000); expect(retryAfter(null)).toBeNull(); expect(retryAfter('bad')).toBeNull()
  expect(retryAfter('Thu, 01 Jan 2026 00:00:05 GMT', Date.parse('2026-01-01T00:00:00Z'))).toBe(5000)
  expect(() => retryAfter('999999')).toThrow('手动')
})
