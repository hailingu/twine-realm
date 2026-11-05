/** 从确认图派生端口和交互候选；候选不会自行进入已保存图。 */
import type { DatabaseBinding, GraphState, PortEndpoint, ServicePort } from '../contract/generated'

/** 连线手势或表单的本地候选，不拥有服务端绑定 ID。 */
export interface BindingCandidate { key: string; bindingId: string | null; source: PortEndpoint | null; target: PortEndpoint | null; databaseName: string; purpose: DatabaseBinding['purpose'] }

/** @param graph 已确认声明 @param serviceId 服务身份 @returns 仅来自本服务明确版本的端口。 */
export function servicePorts(graph: GraphState, serviceId: string): ServicePort[] { return graph.serviceContracts.find(value => value.serviceId === serviceId)?.document.ports ?? [] }

/** @param graph 已确认声明 @param serviceId 服务身份 @returns 仍缺绑定的具名消费端口。 */
export function missingPorts(graph: GraphState, serviceId: string): string[] { return servicePorts(graph, serviceId).filter(port => port.role === 'consume' && port.required && !graph.bindings.some(binding => binding.source.componentId === serviceId && binding.source.portId === port.id)).map(port => port.id) }

/** @param source 消费端候选 @param target 提供端候选 @param binding 既有重连对象 @returns 保留明确选择的独立交互身份。 */
export function bindingCandidate(source: PortEndpoint | null = null, target: PortEndpoint | null = null, binding?: DatabaseBinding): BindingCandidate {
  return { key: crypto.randomUUID(), bindingId: binding?.id ?? null, source: source ?? binding?.source ?? null, target: target ?? binding?.target ?? null, databaseName: binding?.databaseName ?? (source ? `app_${source.componentId.replaceAll('-', '').slice(0, 12)}` : ''), purpose: binding?.purpose ?? 'readWrite' }
}
