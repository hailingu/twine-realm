/** 部署客户端使用独立契约，预览、受理与真实就绪各自拥有状态。 */
import type { DeployCapability, DeployCommand, DeployPlan, DeployPreviewRequest, DeployReceipt, DeploySnapshot } from '../contract/generated'
import { RequestError, request } from '../project/api'

/** 部署面板消费的唯一服务端边界，不接受SQL、任意命令或秘密。 */
export interface DeploymentAPI {
  capability(signal?: AbortSignal): Promise<DeployCapability>
  snapshot(projectId: string, serviceId: string, signal?: AbortSignal): Promise<DeploySnapshot>
  preview(projectId: string, serviceId: string, command: DeployPreviewRequest, signal?: AbortSignal): Promise<DeployPlan>
  accept(projectId: string, serviceId: string, command: DeployCommand, signal?: AbortSignal): Promise<DeployReceipt>
  receipt(mutationId: string, signal?: AbortSignal): Promise<DeployReceipt>
}

/** @returns 不含外部原文的归属错误。 */
function invalid(): RequestError { return new RequestError({ code: 'INVALID_RESPONSE', message: '部署响应与当前项目、服务或原请求不一致，请核实原请求' }, 0) }

/** @param id 项目 @param serviceId 服务 @returns 受编码的同源路径。 */
function route(id: string, serviceId: string): string { return `/projects/${encodeURIComponent(id)}/services/${encodeURIComponent(serviceId)}` }

/** @param plan 已验证计划 @param id 项目 @param serviceId 服务 @param command 原预览 @returns 归属及输入一致的计划。 */
export function ownedPlan(plan: DeployPlan, id: string, serviceId: string, command: DeployPreviewRequest): DeployPlan {
  const input = plan.request
  if (plan.projectId !== id || plan.serviceId !== serviceId || plan.candidate.source.projectId !== id || plan.candidate.source.serviceId !== serviceId || input.baseRevision !== command.baseRevision || input.baseGeneration !== command.baseGeneration || input.candidateId !== command.candidateId || input.bindingId !== command.bindingId || input.hostPort !== command.hostPort || Object.keys(input.configuration).length !== 0 || Object.keys(command.configuration).length !== 0 || plan.binding.id !== command.bindingId || plan.candidate.id !== command.candidateId || plan.binding.source.componentId !== serviceId || plan.binding.target.componentId !== plan.database.componentId) throw invalid()
  return plan
}

/** @param value 原回执 @param id 项目 @param serviceId 服务 @param command 原命令 @returns 精确对应原预览和代次的回执。 */
export function ownedReceipt(value: DeployReceipt, id: string, serviceId: string, command: DeployCommand): DeployReceipt {
  const target = value.target
  ownedPlan(target.plan, id, serviceId, command.preview)
  if (value.mutationId !== command.mutationId || target.projectId !== id || target.serviceId !== serviceId || target.plan.digest !== command.planDigest || BigInt(target.generation) !== BigInt(command.preview.baseGeneration) + 1n) throw invalid()
  return value
}

/** @param value 已验证快照 @param id 项目 @param serviceId 服务 @returns 交叉指针一致的投影。 */
export function ownedSnapshot(value: DeploySnapshot, id: string, serviceId: string): DeploySnapshot {
  if (value.projectId !== id || value.serviceId !== serviceId) throw invalid()
  const { target, operation, observation } = value
  if (target === null) {
    if (operation !== null || observation !== null || value.generation !== '0') { throw invalid() }
    return value
  }
  ownedPlan(target.plan, id, serviceId, target.plan.request)
  if (target.projectId !== id || target.serviceId !== serviceId || target.generation !== value.generation || operation?.targetId !== target.id || operation.projectId !== id || operation.serviceId !== serviceId) throw invalid()
  if (observation?.ready && (!observation.running || !observation.healthy || observation.state !== 'ready' || observation.migrationIntegrity !== 'verified' || observation.databaseUuid !== target.plan.database.serverUuid || observation.imageId !== target.plan.candidate.image.id || observation.entryUrl !== `http://127.0.0.1:${target.plan.request.hostPort}`)) throw invalid()
  return value
}

/** 真实客户端核实归属后才交给工作台，不向业务入口转发控制面请求头。 */
export const DEPLOYMENT_API: DeploymentAPI = {
  capability: signal => request('/deployment/capabilities', 'DeployCapability', 'GET', undefined, signal, 4),
  snapshot: async (id, serviceId, signal) => ownedSnapshot(await request<DeploySnapshot>(route(id, serviceId) + '/deployment', 'DeploySnapshot', 'GET', undefined, signal, 4), id, serviceId),
  preview: async (id, serviceId, command, signal) => ownedPlan(await request<DeployPlan>(route(id, serviceId) + '/deployment/preview', 'DeployPlan', 'POST', command, signal, 4), id, serviceId, command),
  accept: async (id, serviceId, command, signal) => ownedReceipt(await request<DeployReceipt>(route(id, serviceId) + '/deployments', 'DeployReceipt', 'POST', command, signal, 4), id, serviceId, command),
  receipt: async (id, signal) => {
    const value = await request<DeployReceipt>(`/deployment-mutations/${encodeURIComponent(id)}`, 'DeployReceipt', 'GET', undefined, signal, 4)
    if (value.mutationId !== id) { throw invalid() }
    return value
  }
}
