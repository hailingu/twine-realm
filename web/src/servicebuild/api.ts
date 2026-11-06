/** 构建客户端只使用同源机器契约，不允许用户命令或业务秘密进入控制面请求。 */
import type { BuildCapability, BuildCancelCommand, BuildCommand, BuildOperation, BuildReceipt, BuildSnapshot } from '../contract/generated'
import { RequestError, request } from '../project/api'
import type { BuildIntent } from './model'
import { boundImageID } from './imageProof'

/** 构建读取与明确命令是界面唯一副作用入口。 */
export interface BuildAPI {
  capability(signal?: AbortSignal): Promise<BuildCapability>
  snapshot(projectId: string, serviceId: string, signal?: AbortSignal): Promise<BuildSnapshot>
  accept(projectId: string, serviceId: string, command: BuildCommand, signal?: AbortSignal): Promise<BuildReceipt>
  cancel(projectId: string, serviceId: string, operationId: string, command: BuildCancelCommand, signal?: AbortSignal): Promise<BuildReceipt>
  receipt(mutationId: string, signal?: AbortSignal): Promise<BuildReceipt>
}

/** @returns 不暴露原始工具输出的边界诊断。 */
function invalid(): RequestError { return new RequestError({ code: 'INVALID_RESPONSE', message: '构建响应与原请求归属或版本不一致，请核实原请求' }, 0) }

/** @param projectId 项目 @param serviceId 服务 @returns 受编码的同源相对构建路由。 */
function route(projectId: string, serviceId: string): string { return `/projects/${encodeURIComponent(projectId)}/services/${encodeURIComponent(serviceId)}` }

/** @param value 已通过schema的投影 @param projectId 项目 @param serviceId 服务 @returns 指针与内容身份一致的投影。 */
function snapshot(value: BuildSnapshot, projectId: string, serviceId: string): BuildSnapshot {
  if (value.projectId !== projectId || value.serviceId !== serviceId || value.currentSource.projectId !== projectId || value.currentSource.serviceId !== serviceId) throw invalid()
  if (value.target === null) {
    if (value.generation !== '0' || value.operation !== null || value.candidate !== null || value.candidateCurrent) { throw invalid() }
    return value
  }
  const operation = value.operation
  if (value.target.projectId !== projectId || value.target.serviceId !== serviceId || value.target.generation !== value.generation || operation?.projectId !== projectId || operation.serviceId !== serviceId || operation.targetId !== value.target.id) throw invalid()
  if (operation.candidate !== null && (!boundImageID(operation.candidate.image) || operation.state !== 'succeeded' || operation.candidate.operationId !== operation.id || operation.candidate.targetId !== operation.targetId)) throw invalid()
  const candidate = value.candidate
  if (candidate !== null && (candidate.source.projectId !== projectId || candidate.source.serviceId !== serviceId || !boundImageID(candidate.image))) throw invalid()
  if (value.candidateCurrent) validateCurrentCandidate(value, operation)
  return value
}

/** @param value 原键回执 @param projectId 路由项目 @param serviceId 路由服务 @param mutationId 原键 @returns 归属一致的受理结果。 */
export function ownedReceipt(value: BuildReceipt, projectId: string, serviceId: string, mutationId: string): BuildReceipt {
  if (value.mutationId !== mutationId || value.target.projectId !== projectId || value.target.serviceId !== serviceId || value.target.source.projectId !== projectId || value.target.source.serviceId !== serviceId) throw invalid()
  return value
}

/** 真实客户端只受理固定输入；202不能当作构建成功。 */
export const BUILD_API: BuildAPI = {
  capability: signal => request('/build/capabilities', 'BuildCapability', 'GET', undefined, signal, 3),
  snapshot: async (id, serviceId, signal) => snapshot(await request<BuildSnapshot>(route(id, serviceId) + '/build', 'BuildSnapshot', 'GET', undefined, signal, 3), id, serviceId),
  accept: async (id, serviceId, command, signal) => intentReceipt(await request<BuildReceipt>(route(id, serviceId) + '/builds', 'BuildReceipt', 'POST', command, signal, 3), id, serviceId, { kind: 'build', command }),
  cancel: async (id, serviceId, operationId, command, signal) => intentReceipt(await request<BuildReceipt>(route(id, serviceId) + `/builds/${encodeURIComponent(operationId)}/cancel`, 'BuildReceipt', 'POST', command, signal, 3), id, serviceId, { kind: 'cancelBuild', operationId, command }),
  receipt: async (id, signal) => {
    const value = await request<BuildReceipt>(`/build-mutations/${encodeURIComponent(id)}`, 'BuildReceipt', 'GET', undefined, signal, 3)
    if (value.mutationId !== id) throw invalid()
    return value
  }
}

/** @param value 已验证回执 @param projectId 项目 @param serviceId 服务 @param intent 原始意图 @returns 完整归属与原命令相符的回执。 */
export function intentReceipt(value: BuildReceipt, projectId: string, serviceId: string, intent: BuildIntent): BuildReceipt {
  ownedReceipt(value, projectId, serviceId, intent.command.mutationId)
  if (value.kind !== intent.kind) throw invalid()
  if (intent.kind === 'cancelBuild') {
    if (value.operationId !== intent.operationId || BigInt(value.version) <= BigInt(intent.command.baseVersion)) throw invalid()
    return value
  }
  const command = intent.command
  if (value.target.source.pinnedCommit !== command.commit || value.target.canvasRevision !== command.baseRevision || BigInt(value.target.generation) !== BigInt(command.baseGeneration) + 1n || value.target.profile.id !== command.profile.id || value.target.profile.version !== command.profile.version || value.target.profile.digest !== command.profile.digest) throw invalid()
  return value
}

/** @param value 已验证投影 @param operation 当前操作 @returns 只允许同一成功操作的当前输入候选。 */
function validateCurrentCandidate(value: BuildSnapshot, operation: BuildOperation): void {
  const candidate = value.candidate
  if (candidate === null || value.target === null) throw invalid()
  if (operation.id !== candidate.operationId || operation.state !== 'succeeded' || candidate.inputDigest !== value.target.inputDigest || candidate.source.pinnedCommit !== value.currentSource.pinnedCommit || value.candidateCode !== 'CURRENT_INPUT') throw invalid()
}
