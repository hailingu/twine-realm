/** 运行测试只构造非敏感发布契约；真实资源与数据恢复由Compose验收负责。 */
import { vi } from 'vitest'
import type { RuntimeCapability, RuntimeCommand, RuntimeReceipt, RuntimeSnapshot } from '../contract/generated'
import { MYSQL_REFERENCE } from '../contract/catalog.generated'
import type { RuntimeAPI } from '../runtime/api'
import { INITIAL_PROJECT, MYSQL_NODE } from './projectFixtures'

const resourceId = '00000000-0000-4000-8000-000000000041'
const time = '2026-11-05T09:00:00Z'
const name = 'tr-' + 'a'.repeat(32)
/** 固定镜像来自已发布目录，夹具不接受任意镜像和命令。 */
export const RUNTIME_IMAGE = 'mysql:9.7.4@sha256:df90258da46f6e7ff8574ff84f17618aba0765cac749928299c69fd4e7cb8ad8'
/** 未受理之前的真实空投影形态。 */
export const EMPTY_RUNTIME: RuntimeSnapshot = { schemaVersion: 1, projectId: INITIAL_PROJECT.id, generation: '0', sequence: '0', target: null, operation: null, observation: null, freshness: 'unknown', observationCode: '', lastAttemptAt: '' }
/** 原始意图身份在未知和重试之间保持稳定。 */
export const RUNTIME_COMMAND: RuntimeCommand = { mutationId: '00000000-0000-4000-8000-000000000043', baseRevision: '1', baseGeneration: '0', desired: 'running' }
/** 本机能力与实际运行事实分别发布。 */
export const RUNTIME_CAPABILITY: RuntimeCapability = { schemaVersion: 1, enabled: true, adapter: 'file-secrets-v1', code: 'AVAILABLE', message: '本机运行能力已核实', image: RUNTIME_IMAGE, composeVersion: '5.6.1' }
/** 已受理回执只记录目标与操作身份，不声称数据库已经就绪。 */
export const RUNTIME_RECEIPT: RuntimeReceipt = {
  schemaVersion: 1, mutationId: RUNTIME_COMMAND.mutationId, operationId: '00000000-0000-4000-8000-000000000044', replayed: false,
  target: { id: '00000000-0000-4000-8000-000000000045', projectId: INITIAL_PROJECT.id, canvasRevision: '1', generation: '1', desired: 'running', createdAt: time,
    resource: { id: resourceId, installationId: '00000000-0000-4000-8000-000000000042', daemonId: 'test-daemon', projectId: INITIAL_PROJECT.id, componentId: MYSQL_NODE.id, definition: MYSQL_REFERENCE, configuration: { charset: 'utf8mb4', collation: 'utf8mb4_0900_ai_ci', memoryMiB: 512, runtimeProfile: 'local-compose-v1' }, configurationHash: 'sha256:' + 'b'.repeat(64), image: RUNTIME_IMAGE, network: name + '-net', volume: name + '-data', container: name + '-mysql', secretRef: resourceId } }
}
/** 有认证身份的近期事实，只有测试明确传入此值才可呈现绿色。 */
export const READY_RUNTIME: RuntimeSnapshot = {
  ...EMPTY_RUNTIME, generation: '1', sequence: '16', target: RUNTIME_RECEIPT.target,
  operation: { mutationId: RUNTIME_COMMAND.mutationId, id: RUNTIME_RECEIPT.operationId, projectId: INITIAL_PROJECT.id, generation: '1', state: 'succeeded', version: '15', attempts: 1, code: 'READY', message: '认证就绪已核实', retryOf: '', createdAt: time, updatedAt: time,
    steps: [{ name: 'preflight', state: 'succeeded', code: '', externalId: 'test-daemon', updatedAt: time }, { name: 'container', state: 'succeeded', code: '', externalId: 'c'.repeat(64), updatedAt: time }, { name: 'readiness', state: 'succeeded', code: '', externalId: '00000000-0000-4000-8000-000000000046', updatedAt: time }] },
  observation: { state: 'running', running: true, ready: true, serverUuid: '00000000-0000-4000-8000-000000000046', containerId: 'c'.repeat(64), volumeFound: true, observedAt: time }, freshness: 'fresh', lastAttemptAt: time
}

/** @returns 默认空投影，具体失败和迟到由测试按时序注入的端口。 */
export function fakeRuntimeAPI() {
  return {
    capability: vi.fn<RuntimeAPI['capability']>().mockResolvedValue(RUNTIME_CAPABILITY),
    snapshot: vi.fn<RuntimeAPI['snapshot']>().mockResolvedValue(EMPTY_RUNTIME),
    accept: vi.fn<RuntimeAPI['accept']>().mockImplementation((_id, _component, command) => Promise.resolve({ ...RUNTIME_RECEIPT, mutationId: command.mutationId, target: { ...RUNTIME_RECEIPT.target, canvasRevision: command.baseRevision, generation: (BigInt(command.baseGeneration) + 1n).toString(), desired: command.desired } })),
    receipt: vi.fn<RuntimeAPI['receipt']>().mockRejectedValue(new Error('尚未提供原运行回执')),
    events: vi.fn<RuntimeAPI['events']>().mockResolvedValue({ schemaVersion: 1, events: [], nextSequence: '0', hasMore: false })
  }
}
