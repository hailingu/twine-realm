/** 状态夹具只模拟公开读取，不冒称真实MySQL事务或Docker运行。 */
import { vi } from 'vitest'
import type { StateEvent, StateSnapshot } from '../contract/generated'
import type { StateAPI } from '../projectstate/api'
import type { StateMessage } from '../projectstate/model'
import { INITIAL_PROJECT, EMPTY_GRAPH } from './projectFixtures'
import { EMPTY_BUILD } from './buildFixtures'
import { EMPTY_DEPLOYMENT } from './deploymentFixtures'
import { EMPTY_RUNTIME } from './runtimeFixtures'

/** @param cursor 独立全项目游标 @param revision 独立画布版本 @returns 完整且可通过schema的同库投影形态。 */
export function stateSnapshot(cursor = '1', revision = '1'): StateSnapshot {
  return { schemaVersion: 1, project: { ...INITIAL_PROJECT, revision }, graph: EMPTY_GRAPH, runtime: EMPTY_RUNTIME, builds: [{ ...EMPTY_BUILD, candidateCurrent: false, currentSource: { ...EMPTY_BUILD.currentSource, projectRevision: revision } }], deployments: [EMPTY_DEPLOYMENT], cursor, replayFloor: '0', capturedAt: '2026-11-06T17:00:00Z', policy: { heartbeatSeconds: 15, idleSeconds: 45, staleSeconds: 20, retentionSeconds: 86400, maxEvents: 100000, pageSize: 200, maxFrameBytes: 4096, maxSnapshotBytes: 67108864, maxConnections: 16, maxProjectConnections: 4, retrySeconds: 2 } }
}

/** @param sequence 实际通知游标 @returns 无编辑确认能力的安全通知。 */
export function stateEvent(sequence: string): StateEvent { return { schemaVersion: 1, id: `${INITIAL_PROJECT.id}:${sequence}`, projectId: INITIAL_PROJECT.id, sequence, scope: 'project', revision: '1', reference: null, occurredAt: '2026-11-06T17:00:00Z' } }

/** 每条受控流保留自己的回调与取消信号，验证迟到围栏而不是模拟资源事实。 */
export interface ControlledStateStream { cursor: string; signal: AbortSignal; emit: (message: StateMessage) => void; fail: (error: Error) => void }

/** @returns 可显式交付、断开或取消的独立读取端口。 */
export function controlledStateAPI() {
  const streams: ControlledStateStream[] = []
  const api = {
    snapshot: vi.fn<StateAPI['snapshot']>().mockResolvedValue(stateSnapshot()),
    events: vi.fn<StateAPI['events']>().mockImplementation((_id, cursor, signal, receive) => {
      let release = () => { /* Promise构造器将同步设置实际释放者。 */ }
      const pending = new Promise<void>((resolve, reject) => {
        release = resolve
        streams.push({ cursor, signal, emit: message => { try { receive(message) } catch (error) { reject(error instanceof Error ? error : new Error('受控状态流拒绝')) } }, fail: reject })
        signal.addEventListener('abort', release, { once: true }); if (signal.aborted) release()
      })
      return pending.finally(() => { signal.removeEventListener('abort', release) })
    })
  }
  return { api, streams }
}

/** @param values 受控流列表 @param index 预期拥有者 @returns 缺失时显式失败，不可用可选调用跳过断言。 */
export function stateStream(values: ControlledStateStream[], index = 0): ControlledStateStream { const value = values[index]; if (!value) throw new Error('尚无预期状态流'); return value }
