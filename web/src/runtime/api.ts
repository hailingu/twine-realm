/** 运行客户端复用同源HTTP与生成校验器；受理回执不代表外部执行已经成功。 */
import type { RuntimeCapability, RuntimeCommand, RuntimeEventPage, RuntimeReceipt, RuntimeSnapshot } from '../contract/generated'
import { MYSQL_REFERENCE } from '../contract/catalog.generated'
import { RequestError, request } from '../project/api'

/** 运行投影与明确目标的唯一副作用端口，不提供删除卷或任意Docker命令。 */
export interface RuntimeAPI {
  capability(signal?: AbortSignal): Promise<RuntimeCapability>
  snapshot(projectId: string, signal?: AbortSignal): Promise<RuntimeSnapshot>
  accept(projectId: string, componentId: string, command: RuntimeCommand, signal?: AbortSignal): Promise<RuntimeReceipt>
  receipt(mutationId: string, signal?: AbortSignal): Promise<RuntimeReceipt>
  events(projectId: string, sequence: string, signal?: AbortSignal): Promise<RuntimeEventPage>
}

/** @param value 固定定义引用 @returns 当前页面能理解的唯一MySQL目录版本。 */
function supported(value: RuntimeReceipt['target']['resource']['definition']): boolean {
  return value.id === MYSQL_REFERENCE.id && value.version === MYSQL_REFERENCE.version && value.digest === MYSQL_REFERENCE.digest
}

/** @returns 不含原始响应或秘密的提交未知诊断。 */
function invalid(): RequestError { return new RequestError({ code: 'INVALID_RESPONSE', message: '运行响应的身份或版本不一致，请核实原请求' }, 0) }

/** @param value 生成schema已验证的投影 @param id 路由项目 @returns 归属和当前指针一致的投影。 */
function snapshot(value: RuntimeSnapshot, id: string): RuntimeSnapshot {
  if (value.projectId !== id) throw invalid()
  if (value.target === null) {
    if (value.generation !== '0' || value.operation !== null || value.observation !== null) throw invalid()
    return value
  }
  if (!value.operation || value.target.projectId !== id || value.target.generation !== value.generation || value.target.resource.projectId !== id || value.operation.projectId !== id || value.operation.generation !== value.generation || !supported(value.target.resource.definition)) throw invalid()
  const fact = value.observation
  if (fact && ((fact.ready && (!fact.running || !fact.volumeFound || !fact.serverUuid || !fact.containerId)) || (fact.state === 'running' && !fact.running) || ((fact.state === 'stopped' || fact.state === 'not_created') && fact.running))) throw invalid()
  return value
}

/** 真实运行客户端只使用相对同源路径，不向业务应用转发控制面请求。 */
export const RUNTIME_API: RuntimeAPI = {
  capability: signal => request('/runtime/capabilities', 'RuntimeCapability', 'GET', undefined, signal),
  snapshot: async (id, signal) => snapshot(await request<RuntimeSnapshot>(`/projects/${encodeURIComponent(id)}/runtime`, 'RuntimeSnapshot', 'GET', undefined, signal), id),
  accept: async (id, componentId, command, signal) => {
    const value = await request<RuntimeReceipt>(`/projects/${encodeURIComponent(id)}/runtime/${encodeURIComponent(componentId)}/targets`, 'RuntimeReceipt', 'POST', command, signal)
    if (value.mutationId !== command.mutationId || value.target.projectId !== id || value.target.resource.projectId !== id || value.target.resource.componentId !== componentId || value.target.canvasRevision !== command.baseRevision || value.target.desired !== command.desired || BigInt(value.target.generation) !== BigInt(command.baseGeneration) + 1n || !supported(value.target.resource.definition)) throw invalid()
    return value
  },
  receipt: async (id, signal) => {
    const value = await request<RuntimeReceipt>(`/runtime-mutations/${encodeURIComponent(id)}`, 'RuntimeReceipt', 'GET', undefined, signal)
    if (value.mutationId !== id || !supported(value.target.resource.definition)) throw invalid()
    return value
  },
  events: (id, sequence, signal) => request(`/projects/${encodeURIComponent(id)}/runtime/events?after=${encodeURIComponent(sequence)}`, 'RuntimeEventPage', 'GET', undefined, signal)
}
