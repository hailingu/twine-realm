/** 项目父命令只使用原计划与原请求键，回执不代表资源已停止。 */
import type { ProjectRunCommand, ProjectRunReceipt } from '../contract/generated'
import { request, RequestError } from '../project/api'

/** 可注入的明确命令端口不拥有 SSE 连接。 */
export interface LifecycleAPI {
  accept(this: void, id: string, command: ProjectRunCommand): Promise<ProjectRunReceipt>
  retry(this: void, id: string, mutationId: string): Promise<ProjectRunReceipt>
  receipt(this: void, id: string, mutationId: string): Promise<ProjectRunReceipt>
}
/** @param value 回执 @param id 项目 @param command 原请求 @returns 归属核实后的回执。 */
export function checkReceipt(value: ProjectRunReceipt, id: string, command: ProjectRunCommand): ProjectRunReceipt {
  if (value.mutationId !== command.mutationId || value.target.projectId !== id || value.target.desired !== command.desired || BigInt(value.target.generation) !== BigInt(command.baseGeneration) + 1n) throw new RequestError({ code: 'INVALID_RESPONSE', message: '原请求回执归属不一致，请继续核实' }, 0)
  return value
}
/** 同源命令在页面离开后仍由后台持久拥有。 */
export const LIFECYCLE_API: LifecycleAPI = {
  retry: (id, mutationId) => request(`/projects/${encodeURIComponent(id)}/lifecycle/receipts/${encodeURIComponent(mutationId)}/retry`, 'ProjectRunReceipt', 'POST', undefined, undefined, 6),
  accept: (id, command) => request(`/projects/${encodeURIComponent(id)}/lifecycle`, 'ProjectRunReceipt', 'POST', command, undefined, 6),
  receipt: (id, mutationId) => request(`/projects/${encodeURIComponent(id)}/lifecycle/receipts/${encodeURIComponent(mutationId)}`, 'ProjectRunReceipt', 'GET', undefined, undefined, 6)
}
