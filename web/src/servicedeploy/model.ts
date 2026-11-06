/** 部署快照单调比较独立代次、事件和操作版本，防止晚响应覆盖新状态。 */
import type { DeploySnapshot } from '../contract/generated'

/** @param state 持久操作状态 @returns 是否仍需后台执行。 */
export function terminalDeployment(state: string): boolean { return state === 'succeeded' || state === 'failed' || state === 'needs_attention' }

/** @param current 已应用投影 @param incoming 新投影 @returns 不回退已观察事实的投影。 */
export function newerSnapshot(current: DeploySnapshot | null, incoming: DeploySnapshot): DeploySnapshot {
  if (!current) return incoming
  if (current.projectId !== incoming.projectId || current.serviceId !== incoming.serviceId) return current
  if (BigInt(incoming.generation) < BigInt(current.generation)) return current
  if (incoming.generation !== current.generation) return incoming
  if (BigInt(incoming.sequence) < BigInt(current.sequence)) return current
  if (current.operation && incoming.operation && (current.operation.id !== incoming.operation.id || BigInt(incoming.operation.version) < BigInt(current.operation.version))) return current
  if (current.lastAttemptAt && (!incoming.lastAttemptAt || Date.parse(incoming.lastAttemptAt) < Date.parse(current.lastAttemptAt))) return current
  return incoming
}
