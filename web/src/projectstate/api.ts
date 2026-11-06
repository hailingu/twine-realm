/** 同源有界状态读取与fetch流，不创建令牌、账号会话或独立编辑真相。 */
import type { ErrorResponse, StateSnapshot } from '../contract/generated'
import { parseContract, RequestError } from '../project/api'
import { invalidState, ownedState, parseStateMessage, type StateMessage } from './model'
import { SSEParser } from './sseParser'

/** 明确窗口恢复、限流等待及不支持的重试期限，不静默忽略Retry-After。 */
export class StateReadError extends Error {
  /** @param message 安全文案 @param resync 是否须完整恢复 @param retryMs 服务指定等待 @param terminal 是否等待用户重新连接。 */
  constructor(message: string, readonly resync = false, readonly retryMs: number | null = null, readonly terminal = false, options?: ErrorOptions) { super(message, options); this.name = 'StateReadError' }
}

/** 读取所有者只消费快照与消息，不能发送停止、取消或编辑命令。 */
export interface StateAPI {
  snapshot(id: string, signal: AbortSignal): Promise<StateSnapshot>
  events(id: string, cursor: string, signal: AbortSignal, receive: (message: StateMessage) => void): Promise<void>
}

/** @param response HTTP运输 @param maximum 字节上限 @returns 受限且完整的JSON值。 */
async function boundedJSON(response: Response, maximum: number): Promise<unknown> {
  const reader = response.body?.getReader(); if (!reader) throw invalidState()
  const chunks: Uint8Array[] = []; let total = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      total += value.length
      if (total > maximum) throw invalidState()
      chunks.push(value)
    }
    const data = new Uint8Array(total); let offset = 0
    for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.length }
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(data)) as unknown
  } finally { await reader.cancel(); reader.releaseLock() }
}

/** @param value HTTP Retry-After @param now 当前时钟 @returns 可表示的秒数或HTTP日期等待。 */
export function retryAfter(value: string | null, now = Date.now()): number | null {
  if (value === null) return null
  const milliseconds = /^\d+$/.test(value) ? Number(value) * 1000 : Date.parse(value) - now
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return null
  if (milliseconds > 3600000) throw new StateReadError('服务要求的等待超过当前自动重连范围，请稍后手动重新连接', false, null, true)
  return milliseconds
}

/** @param response 非成功HTTP响应 @returns 保留服务端重试期限和完整恢复要求的安全错误。 */
async function responseFailure(response: Response): Promise<StateReadError> {
  const wait = retryAfter(response.headers.get('Retry-After'))
  const value = parseContract<ErrorResponse>('ErrorResponse', await boundedJSON(response, 128 * 1024))
  return new StateReadError(value.error.message, ['STATE_CURSOR_EXPIRED', 'STATE_CURSOR_AHEAD', 'STATE_EVENT_GAP'].includes(value.error.code), wait)
}

/** @param id 项目 @param signal 读取寿命 @returns 同库完整信封，不将多个旧接口拼接成快照。 */
async function snapshot(id: string, signal: AbortSignal): Promise<StateSnapshot> {
  const response = await fetch(`/api/v5/projects/${encodeURIComponent(id)}/state`, { signal: AbortSignal.any([signal, AbortSignal.timeout(12000)]), cache: 'no-store', redirect: 'error' })
  if (!response.ok) throw await responseFailure(response)
  if (response.status !== 200) throw invalidState()
  return ownedState(await boundedJSON(response, 64 * 1024 * 1024), id)
}

/** @param id 项目 @param cursor 实际已重放游标 @param signal 读取寿命 @param receive 当前拥有者 @returns 正常EOF同样交给重连拥有者核实。 */
async function events(id: string, cursor: string, signal: AbortSignal, receive: (message: StateMessage) => void): Promise<void> {
  const idle = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined
  const touch = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = setTimeout(() => { idle.abort() }, 45000)
  }
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  let failure: StateReadError | null = null
  touch()
  try {
    const response = await fetch(`/api/v5/projects/${encodeURIComponent(id)}/state/events`, { headers: { Accept: 'text/event-stream', 'Last-Event-ID': cursor }, signal: AbortSignal.any([signal, idle.signal]), cache: 'no-store', redirect: 'error' })
    if (!response.ok) throw await responseFailure(response)
    if (response.status !== 200 || !/^text\/event-stream(?:;|$)/i.test(response.headers.get('Content-Type') ?? '') || !response.body) throw invalidState()
    reader = response.body.getReader()
    const parser = new SSEParser(4096, frame => { const data: unknown = JSON.parse(frame.data); receive(parseStateMessage(frame.event, id, frame.id, data)) })
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      touch(); parser.push(value)
    }
    parser.finish()
    throw new StateReadError('状态连接已断开，保留最后事实并按原游标重连')
  } catch (error) {
    failure = stateReadFailure(error)
  } finally {
    if (timer !== undefined) clearTimeout(timer)
    if (reader) {
      try { await reader.cancel() }
      catch (error) { failure = new StateReadError(failure?.message ?? '状态读取结束时尚需重新核实', failure?.resync ?? false, failure?.retryMs ?? null, failure?.terminal ?? false, { cause: new AggregateError([failure, error], '读取与回收结果已保留') }) }
      finally { reader.releaseLock() }
    }
  }
  throw failure ?? new StateReadError('状态读取已结束，请重新核实')
}

/** @param error 当前读取故障 @returns 协议错误要求完整恢复，普通断网保留实际游标。 */
function stateReadFailure(error: unknown): StateReadError {
  if (error instanceof StateReadError) return error
  if (error instanceof RequestError || error instanceof SyntaxError) return new StateReadError('状态协议尚未确认，保留草稿并读取完整信封', true)
  return new StateReadError('状态连接暂不可用，正在保留事实并退避重连')
}

/** 真实浏览器只调用同源状态读取，所有取消仅属于本次读取连接。 */
export const STATE_API: StateAPI = { snapshot, events }
