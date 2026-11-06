/** 构建投影维护独立版本，不能覆盖画布草稿或退回旧操作。 */
import type { BuildCapability, BuildCancelCommand, BuildCommand, BuildReceipt, BuildSnapshot } from '../contract/generated'

/** 原意图在提交未知期间保持同键和原路由。 */
export type BuildIntent = { kind: 'build'; command: BuildCommand } | { kind: 'cancelBuild'; operationId: string; command: BuildCancelCommand }
/** 提交状态与持久构建操作状态分别拥有者。 */
export type BuildSubmission = { kind: 'idle' } | { kind: 'sending' | 'unknown'; intent: BuildIntent; message: string } | { kind: 'rejected'; message: string }
/** 单一服务面板拥有的投影与原键恢复状态。 */
export interface BuildState { snapshot: BuildSnapshot | null; capability: BuildCapability | null; readError: string | null; submission: BuildSubmission; receipt: BuildReceipt | null }
/** 每个完成都带原键，迟到响应不能解除另一请求的保护。 */
export type BuildAction = { type: 'snapshot'; value: BuildSnapshot } | { type: 'capability'; value: BuildCapability } | { type: 'readFailed'; message: string } | { type: 'sending'; intent: BuildIntent } | { type: 'unknown' | 'rejected'; id: string; message: string } | { type: 'accepted'; id: string; receipt: BuildReceipt }

/** @returns 未读服务前的明确空状态。 */
export function initialBuild(): BuildState { return { snapshot: null, capability: null, readError: null, submission: { kind: 'idle' }, receipt: null } }

/** @param previous 旧投影 @param next 新投影 @returns 是否可以前进版本；源码revision单独约束。 */
function newer(previous: BuildSnapshot | null, next: BuildSnapshot): boolean {
  if (!previous) return true
  if (BigInt(next.currentSource.projectRevision) < BigInt(previous.currentSource.projectRevision) || BigInt(next.generation) < BigInt(previous.generation)) return false
  return next.generation !== previous.generation || BigInt(next.operation?.version ?? '0') >= BigInt(previous.operation?.version ?? '0')
}

/** @param state 原构建状态 @param action 有归属的异步完成 @returns 保留原键且版本不倒退的投影。 */
export function buildReducer(state: BuildState, action: BuildAction): BuildState {
  switch (action.type) {
    case 'snapshot': return newer(state.snapshot, action.value) ? { ...state, snapshot: action.value, readError: null } : state
    case 'capability': return { ...state, capability: action.value }
    case 'readFailed': return { ...state, readError: action.message }
    case 'sending': {
      const active = state.submission
      if ((active.kind === 'unknown' || active.kind === 'sending') && active.intent.command.mutationId !== action.intent.command.mutationId) return state
      return { ...state, submission: { kind: 'sending', intent: action.intent, message: '' } }
    }
    default: {
      const active = state.submission
      if ((active.kind !== 'sending' && active.kind !== 'unknown') || active.intent.command.mutationId !== action.id) return state
      if (action.type === 'accepted') return { ...state, receipt: action.receipt, submission: { kind: 'idle' } }
      return { ...state, submission: action.type === 'unknown' ? { kind: 'unknown', intent: active.intent, message: action.message } : { kind: 'rejected', message: action.message } }
    }
  }
}

/** @param state 持久操作状态 @returns 是否为已确认终态；未知操作只能明确核实/取消。 */
export function terminalBuild(state: string): boolean { return ['succeeded', 'failed', 'cancelled', 'needs_attention'].includes(state) }
