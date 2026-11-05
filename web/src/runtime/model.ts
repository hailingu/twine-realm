/** 运行投影只接受版本前进，原请求身份与外部操作状态分别维护。 */
import type { RuntimeCapability, RuntimeCommand, RuntimeReceipt, RuntimeSnapshot } from '../contract/generated'

/** 一次受理请求的状态，未知结果保留原命令而不是自动换键。 */
export type Submission = { kind: 'idle' } | { kind: 'sending'; command: RuntimeCommand } | { kind: 'unknown'; command: RuntimeCommand; message: string; verifying: boolean } | { kind: 'accepted'; command: RuntimeCommand; receipt: RuntimeReceipt } | { kind: 'rejected'; id: string; message: string }

/** 工作区拥有运行投影，节点和详情只消费同一份确认状态。 */
export interface RuntimeState { capability: RuntimeCapability | null; snapshot: RuntimeSnapshot | null; readError: string | null; submission: Submission; lastReceipt: RuntimeReceipt | null; now: number }

/** 每个异步完成都携带原请求身份，迟到响应不能解除另一请求的保护。 */
export type RuntimeAction = { type: 'clock'; now: number } | { type: 'capability'; value: RuntimeCapability } | { type: 'snapshot'; value: RuntimeSnapshot } | { type: 'readFailed'; message: string } | { type: 'sending'; command: RuntimeCommand } | { type: 'unknown'; id: string; message: string } | { type: 'verifying'; id: string } | { type: 'accepted'; id: string; receipt: RuntimeReceipt } | { type: 'rejected'; id: string; message: string }

/** @returns 未读取真实事实前的明确空状态。 */
export function initialRuntime(): RuntimeState { return { capability: null, snapshot: null, readError: null, submission: { kind: 'idle' }, lastReceipt: null, now: 0 } }

/** @param submission 当前受理状态 @returns 仍需保护的原请求身份。 */
function requestID(submission: Submission): string | null { return submission.kind === 'idle' || submission.kind === 'rejected' ? null : submission.command.mutationId }

/** @param state 当前确认投影 @param action 有归属的完成 @returns 不回退版本或丢失原键的下一状态。 */
export function runtimeReducer(state: RuntimeState, action: RuntimeAction): RuntimeState {
  switch (action.type) {
    case 'clock': return { ...state, now: Math.max(state.now, action.now) }
    case 'capability': return { ...state, capability: action.value }
    case 'readFailed': return { ...state, readError: action.message }
    case 'snapshot': return acceptSnapshot(state, action.value)
    case 'sending': return beginSubmission(state, action.command)
    default: return finishSubmission(state, action)
  }
}

/** @param state 原投影 @param command 原请求 @returns 仅在没有其他所有者时开始或重试同一提交。 */
function beginSubmission(state: RuntimeState, command: RuntimeCommand): RuntimeState {
  const owner = requestID(state.submission)
  return owner && owner !== command.mutationId ? state : { ...state, submission: { kind: 'sending', command } }
}

/** @param state 原投影 @param action 有原请求身份的完成 @returns 只解除该请求自己的保护。 */
function finishSubmission(state: RuntimeState, action: Extract<RuntimeAction, { id: string }>): RuntimeState {
  if (requestID(state.submission) !== action.id || state.submission.kind === 'idle' || state.submission.kind === 'rejected') return state
  const command = state.submission.command
  if (action.type === 'unknown') return { ...state, submission: { kind: 'unknown', command, message: action.message, verifying: false } }
  if (action.type === 'verifying') return state.submission.kind === 'unknown' ? { ...state, submission: { ...state.submission, verifying: true } } : state
  if (action.type === 'rejected') return { ...state, submission: { kind: 'rejected', id: action.id, message: action.message } }
  const settled = state.snapshot && BigInt(state.snapshot.generation) >= BigInt(action.receipt.target.generation)
  return { ...state, lastReceipt: action.receipt, submission: settled ? { kind: 'idle' } : { kind: 'accepted', command, receipt: action.receipt } }
}

/** @param state 原投影 @param value 已验证新投影 @returns 代次、事件及采集时间不回退的投影。 */
function acceptSnapshot(state: RuntimeState, value: RuntimeSnapshot): RuntimeState {
  const previous = state.snapshot
  if (previous) {
    if (BigInt(value.generation) < BigInt(previous.generation) || BigInt(value.sequence) < BigInt(previous.sequence)) return state
    if (value.generation === previous.generation && value.sequence === previous.sequence && (Date.parse(value.lastAttemptAt) || 0) < (Date.parse(previous.lastAttemptAt) || 0)) return state
  }
  const submission = state.submission.kind === 'accepted' && BigInt(value.generation) >= BigInt(state.submission.receipt.target.generation) ? { kind: 'idle' as const } : state.submission
  return { ...state, snapshot: value, readError: null, submission }
}

/** @param state 当前运行投影 @returns 是否存在尚未确认的受理请求。 */
export function submissionPending(state: RuntimeState): boolean { return state.submission.kind !== 'idle' && state.submission.kind !== 'rejected' }

/** @param state 同一工作区投影 @param componentId 画布组件 @param now 当前呈现时刻 @returns 区分事实与未知的节点文字。 */
export function runtimeLabel(state: RuntimeState, componentId: string, now: number): { text: string; tone: string } {
  const value = state.snapshot
  if (!state.capability) return { text: '运行能力未读取', tone: 'unknown' }
  if (!state.capability.enabled) return { text: '运行能力未启用', tone: 'unknown' }
  if (state.readError || !value) return { text: '运行事实未知', tone: 'unknown' }
  if (value.target?.resource.componentId !== componentId) return { text: '尚未登记运行目标', tone: 'unknown' }
  const fact = value.observation
  if (!fact || value.freshness === 'unknown') return { text: '运行事实未知', tone: 'unknown' }
  if (value.freshness === 'stale' || now - Date.parse(fact.observedAt) > 20000) return { text: '运行观测已过期', tone: 'unknown' }
  if (value.observationCode || fact.state === 'drifted') return { text: '运行资源需核实', tone: 'error' }
  if (fact.ready && fact.running) return { text: '实际运行 · 认证就绪', tone: 'ready' }
  if (fact.running) return { text: '运行中 · 尚未就绪', tone: 'pending' }
  return { text: fact.state === 'not_created' ? '未发现运行容器' : '实际已停止', tone: 'stopped' }
}
