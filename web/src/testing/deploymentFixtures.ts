/** 部署界面夹具只用于协议和交互验证，不冒称真实Docker执行。 */
import { vi } from 'vitest'
import type { DeployCapability, DeployCommand, DeployPlan, DeployReceipt, DeploySnapshot } from '../contract/generated'
import type { DeploymentAPI } from '../servicedeploy/api'
import { BUILD_CANDIDATE, BUILD_TARGET } from './buildFixtures'
import { DB_BINDING } from './graphFixtures'
import { READY_RUNTIME, RUNTIME_RECEIPT } from './runtimeFixtures'

const hash = 'sha256:' + 'c'.repeat(64)
const time = '2026-11-06T10:00:00Z'
const resource = RUNTIME_RECEIPT.target.resource
/** 部署能力只表达配置。 */
export const DEPLOY_CAPABILITY: DeployCapability = { schemaVersion: 1, enabled: true, adapter: 'go-mysql-compose-v1', code: 'CONFIGURED', message: '已配置单服务MySQL部署', policy: { id: 'go-mysql-compose-v1', version: 1, totalSeconds: 360, migrationSeconds: 120, readinessSeconds: 120, commandSeconds: 45, leaseSeconds: 420, memoryMiB: 512, tmpfsMiB: 64, pids: 128, cpus: 1 } }
/** 完整公开预览；秘密和宿主材料路径都不在此对象内。 */
export const DEPLOY_PLAN: DeployPlan = {
  schemaVersion: 1, projectId: BUILD_TARGET.projectId, serviceId: BUILD_TARGET.serviceId, serviceName: '验收服务',
  request: { baseRevision: '1', baseGeneration: '0', candidateId: BUILD_CANDIDATE.id, bindingId: DB_BINDING.id, hostPort: 8081, configuration: {} },
  contract: DB_BINDING.sourceDefinition, binding: DB_BINDING, candidate: BUILD_CANDIDATE,
  database: { resourceId: resource.id, installationId: resource.installationId, daemonId: resource.daemonId, componentId: resource.componentId, generation: '1', resourceDigest: hash, configurationHash: resource.configurationHash, serverUuid: READY_RUNTIME.observation!.serverUuid, network: resource.network, networkCidr: '172.30.0.0/16', volume: resource.volume, container: resource.container, image: resource.image },
  environment: { context: 'default', daemonId: resource.daemonId, dockerVersion: '29.9.2', composeVersion: '5.6.1', architecture: 'amd64', bindSourceMode: 'native', uid: 1000, gid: 1000, buildInstallationId: BUILD_TARGET.installationId, runtimeInstallationId: resource.installationId },
  policy: DEPLOY_CAPABILITY.policy,
  resources: { composeProject: resource.container, application: 'tr-' + 'c'.repeat(24) + '-service', migration: 'tr-' + 'c'.repeat(24) + '-migration', ingressNetwork: 'tr-' + 'c'.repeat(24) + '-http', applicationUser: 'tra_' + 'c'.repeat(24), migrationUser: 'trm_' + 'c'.repeat(24), accountHost: '172.30.0.0/255.255.0.0' },
  runtimeDigest: hash, digest: hash
}
/** 未受理前的零代次状态。 */
export const EMPTY_DEPLOYMENT: DeploySnapshot = { schemaVersion: 1, projectId: DEPLOY_PLAN.projectId, serviceId: DEPLOY_PLAN.serviceId, generation: '0', sequence: '0', target: null, operation: null, observation: null, freshness: 'unknown', observationCode: '', lastAttemptAt: '' }
/** 原确认命令用于核实回执归属。 */
export const DEPLOY_COMMAND: DeployCommand = { mutationId: '00000000-0000-4000-8000-000000000091', preview: DEPLOY_PLAN.request, planDigest: DEPLOY_PLAN.digest }
/** 回执只表示受理成功。 */
export const DEPLOY_RECEIPT: DeployReceipt = { schemaVersion: 1, mutationId: DEPLOY_COMMAND.mutationId, operationId: '00000000-0000-4000-8000-000000000092', replayed: false, target: { id: '00000000-0000-4000-8000-000000000093', projectId: DEPLOY_PLAN.projectId, serviceId: DEPLOY_PLAN.serviceId, generation: '1', plan: DEPLOY_PLAN, createdAt: time } }
/** 最近认证与只读探针已确认的运行投影。 */
export const READY_DEPLOYMENT: DeploySnapshot = { ...EMPTY_DEPLOYMENT, generation: '1', sequence: '20', target: DEPLOY_RECEIPT.target, operation: { id: DEPLOY_RECEIPT.operationId, targetId: DEPLOY_RECEIPT.target.id, projectId: DEPLOY_PLAN.projectId, serviceId: DEPLOY_PLAN.serviceId, version: '20', state: 'succeeded', code: 'READY', message: '业务就绪已核实', steps: [{ name: 'readiness', state: 'succeeded', code: '', updatedAt: time }], createdAt: time, updatedAt: time }, observation: { state: 'ready', running: true, healthy: true, ready: true, containerId: 'd'.repeat(64), imageId: DEPLOY_PLAN.candidate.image.id, entryUrl: 'http://127.0.0.1:8081', databaseUuid: DEPLOY_PLAN.database.serverUuid, migrationVersion: 1, migrationIntegrity: 'verified', observedAt: time }, freshness: 'fresh', lastAttemptAt: time }

/** @returns 独立端口记录，未知请求可以按原键配置恢复。 */
export function fakeDeploymentAPI() {
  return {
    capability: vi.fn<DeploymentAPI['capability']>().mockResolvedValue(DEPLOY_CAPABILITY),
    snapshot: vi.fn<DeploymentAPI['snapshot']>().mockResolvedValue(EMPTY_DEPLOYMENT),
    preview: vi.fn<DeploymentAPI['preview']>().mockImplementation((_id, _service, command) => Promise.resolve({ ...DEPLOY_PLAN, request: command })),
    accept: vi.fn<DeploymentAPI['accept']>().mockImplementation((_id, _service, command) => Promise.resolve({ ...DEPLOY_RECEIPT, mutationId: command.mutationId, target: { ...DEPLOY_RECEIPT.target, generation: String(BigInt(command.preview.baseGeneration) + 1n), plan: { ...DEPLOY_PLAN, request: command.preview, digest: command.planDigest } } })),
    receipt: vi.fn<DeploymentAPI['receipt']>().mockResolvedValue({ ...DEPLOY_RECEIPT, replayed: true })
  }
}
