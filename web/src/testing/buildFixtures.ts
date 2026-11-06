/** 构建夹具只含公开模拟身份和摘要，不包含Docker socket或运行秘密。 */
import { vi } from 'vitest'
import type { BuildCandidate, BuildCapability, BuildCommand, BuildOperation, BuildReceipt, BuildSnapshot, BuildTarget } from '../contract/generated'
import type { BuildAPI } from '../servicebuild/api'
import { INITIAL_SOURCE } from './projectFixtures'

const hash = 'sha256:' + 'a'.repeat(64)
const time = '2026-11-06T00:00:00Z'
/** 当前支持组合的公开能力。 */
export const BUILD_CAPABILITY: BuildCapability = { schemaVersion: 1, enabled: true, adapter: 'local-go-container-v1', code: 'CONFIGURED', message: '已配置固定Go工具链', profile: { id: 'go-net-http-mysql', version: '1.0.0', digest: hash }, imageReference: 'golang:1.27.3@' + hash, policy: { id: 'go-container-local-v1', version: 1, totalSeconds: 480, commandSeconds: 180, sourceBytes: 67108864, sourceFiles: 4096, binaryBytes: 134217728, memoryMiB: 4096, tmpfsMiB: 3072, pids: 256, cpus: 2 } }
/** 已固定源码但未接受构建的状态。 */
export const EMPTY_BUILD: BuildSnapshot = { schemaVersion: 1, projectId: INITIAL_SOURCE.version.projectId, serviceId: INITIAL_SOURCE.version.serviceId, generation: '0', target: null, operation: null, candidate: null, candidateCurrent: false, candidateCode: 'NO_CANDIDATE', currentSource: { ...INITIAL_SOURCE.version, pinnedCommit: 'a'.repeat(40) } }
/** 第一次明确构建命令。 */
export const BUILD_COMMAND: BuildCommand = { mutationId: '00000000-0000-4000-8000-000000000080', baseRevision: '1', baseGeneration: '0', commit: 'a'.repeat(40), profile: BUILD_CAPABILITY.profile }
/** 受理后不可变的完整输入，不把布局revision用作源码摘要。 */
export const BUILD_TARGET: BuildTarget = { id: '00000000-0000-4000-8000-000000000081', projectId: EMPTY_BUILD.projectId, serviceId: EMPTY_BUILD.serviceId, generation: '1', canvasRevision: '1', installationId: '00000000-0000-4000-8000-000000000082', source: { ...EMPTY_BUILD.currentSource, pinnedCommit: BUILD_COMMAND.commit }, profile: BUILD_CAPABILITY.profile, environment: { context: 'default', daemonId: 'test-daemon', dockerVersion: '29.9.2', buildxVersion: 'v0.38.1 public-test', builderName: 'default', buildkitVersion: 'v0.34.0', imageId: hash, imageReference: BUILD_CAPABILITY.imageReference, architecture: 'amd64', operatingSystem: 'linux', bindSourceMode: 'canonical', uid: 1000, gid: 1000, endpointDigest: hash }, policy: BUILD_CAPABILITY.policy, inputDigest: hash, createdAt: time }
/** 原请求固定回执，202只表示受理。 */
export const BUILD_RECEIPT: BuildReceipt = { schemaVersion: 1, mutationId: BUILD_COMMAND.mutationId, kind: 'build', operationId: '00000000-0000-4000-8000-000000000083', target: BUILD_TARGET, version: '1', replayed: false }
/** 可轮询的在途构建。 */
export const BUILD_OPERATION: BuildOperation = { id: BUILD_RECEIPT.operationId, targetId: BUILD_TARGET.id, projectId: EMPTY_BUILD.projectId, serviceId: EMPTY_BUILD.serviceId, version: '2', state: 'executing', code: 'EXECUTING', message: '固定输入检查中', cancelRequested: false, steps: [{ name: 'source', state: 'succeeded', code: '', updatedAt: time }], createdAt: time, updatedAt: time, candidate: null }
/** 已受理投影，不冒充候选。 */
export const RUNNING_BUILD: BuildSnapshot = { ...EMPTY_BUILD, generation: '1', target: BUILD_TARGET, operation: BUILD_OPERATION }
/** 完整候选结构用于验证追溯展示与跨字段拒绝。 */
export const BUILD_CANDIDATE: BuildCandidate = { id: '00000000-0000-4000-8000-000000000084', targetId: BUILD_TARGET.id, operationId: BUILD_OPERATION.id, inputDigest: hash, sourceDigest: hash, source: BUILD_TARGET.source, profile: BUILD_TARGET.profile, template: { id: 'go-net-http-mysql-template', version: '1.0.0', digest: hash }, environment: BUILD_TARGET.environment, policy: BUILD_TARGET.policy, checks: [{ name: 'test', state: 'succeeded', toolVersion: 'go1.27.3', outputDigest: hash, completedAt: time }], migrations: [{ path: 'internal/schema/migrations/000001_initial.up.sql', digest: hash }], migrationDigest: hash, image: { id: hash, idKind: 'config', exportDigest: 'sha256:' + 'b'.repeat(64), manifestDigest: 'sha256:' + 'b'.repeat(64), manifestMediaType: 'application/vnd.oci.image.manifest.v1+json', configDigest: hash, tag: 'test-candidate:local', binaryDigest: hash }, createdAt: time }
/** 成功投影仍明确表示未部署。 */
export const READY_BUILD: BuildSnapshot = { ...RUNNING_BUILD, operation: { ...BUILD_OPERATION, version: '32', state: 'succeeded', code: 'BUILT', message: '真实检查已通过', candidate: BUILD_CANDIDATE }, candidate: BUILD_CANDIDATE, candidateCurrent: true, candidateCode: 'CURRENT_INPUT' }

/** @returns 每次测试独立的副作用记录端口。 */
export function fakeBuildAPI() {
  return { capability: vi.fn<BuildAPI['capability']>().mockResolvedValue(BUILD_CAPABILITY), snapshot: vi.fn<BuildAPI['snapshot']>().mockResolvedValue(EMPTY_BUILD), accept: vi.fn<BuildAPI['accept']>().mockImplementation((_id, _service, command) => Promise.resolve({ ...BUILD_RECEIPT, mutationId: command.mutationId })), cancel: vi.fn<BuildAPI['cancel']>().mockImplementation((_id, _service, operation, command) => Promise.resolve({ ...BUILD_RECEIPT, mutationId: command.mutationId, operationId: operation, kind: 'cancelBuild', version: String(BigInt(command.baseVersion) + 1n) })), receipt: vi.fn<BuildAPI['receipt']>().mockResolvedValue({ ...BUILD_RECEIPT, replayed: true }) }
}
