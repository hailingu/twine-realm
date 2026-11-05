/** 草稿位置与确认快照分离，库的节点类型不进入领域计算。 */
import type { NodePosition, Position, Project, ServiceNode } from '../contract/generated'

/** 待提交请求在重试时保持原身份、版本和载荷不变。 */
export type PendingRequest =
  | { kind: 'registerRepository'; projectId: string; command: import('../contract/generated').RegisterCommand }
  | { kind: 'saveLayout'; projectId: string; command: import('../contract/generated').LayoutCommand }
  | { kind: 'pinSource'; projectId: string; serviceId: string; command: import('../contract/generated').PinSourceCommand }

/** 保存状态只有一个所有者，不使用互相矛盾的布尔旗标。 */
export type SaveState =
  | { kind: 'ready' }
  | { kind: 'sending'; request: PendingRequest }
  | { kind: 'unknown'; request: PendingRequest; message: string }
  | { kind: 'rejected'; message: string; conflict: boolean }

/** @param project 已确认快照 @returns 可编辑的位置草稿。 */
export function positionsOf(project: Project): NodePosition[] {
  return project.nodes.map(node => ({ serviceId: node.id, ...node.position }))
}

/** @param positions 草稿 @param id 稳定服务身份 @param position 新位置 @returns 更新后的草稿，越界坐标不进入状态。 */
export function movePosition(positions: NodePosition[], id: string, position: Position): NodePosition[] {
  if (!Number.isFinite(position.x) || !Number.isFinite(position.y) || Math.abs(position.x) > 1000000 || Math.abs(position.y) > 1000000) return positions
  return positions.map(item => item.serviceId === id ? { serviceId: id, ...position } : item)
}

/** @param project 确认快照 @param draft 本地草稿 @returns 是否存在尚未持久化的变化。 */
export function isDirty(project: Project, draft: NodePosition[]): boolean {
  return project.nodes.some(node => { const item = draft.find(value => value.serviceId === node.id); return item?.x !== node.position.x || item?.y !== node.position.y })
}

/** @param nodes 最新服务集合 @param draft 原位置草稿 @returns 显式重放到最新版本的草稿，新节点保持服务端位置。 */
export function rebasePositions(nodes: ServiceNode[], draft: NodePosition[]): NodePosition[] {
  return nodes.map(node => draft.find(item => item.serviceId === node.id) ?? { serviceId: node.id, ...node.position })
}
