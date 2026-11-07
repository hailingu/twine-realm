/** 状态信封的语义边界：元数据游标、领域版本和外部候选核实分别保护。 */
import type { BuildSnapshot, Project, RuntimeSnapshot, GraphState, StateCaughtUp, StateEvent, StateResync, StateSnapshot } from '../contract/generated'
import { parseContract, RequestError } from '../project/api'
import { ownedSnapshot } from '../servicedeploy/api'

/** 已校验的流消息；心跳由运输拥有者维护，不占业务序号。 */
export type StateMessage = { kind: 'state'; value: StateEvent } | { kind: 'caughtUp'; value: StateCaughtUp } | { kind: 'resync'; value: StateResync }

/** @returns 不回显未知载荷的协议失败。 */
export function invalidState(): RequestError { return new RequestError({ code: 'STATE_PROTOCOL_INVALID', message: '状态通知不完整或版本不兼容，正在保留草稿并重新核实' }, 0) }

/** @param value 规范十进制字符串 @returns 完整uint64，拒绝数字截断与溢出。 */
export function stateNumber(value: string): bigint {
  if (!/^(0|[1-9]\d{0,19})$/.test(value)) throw invalidState()
  const result = BigInt(value)
  if (result > 18446744073709551615n) throw invalidState()
  return result
}

/** @param input 已通过schema的信封 @param id 当前项目 @returns 交叉身份与全量成员一致的信封。 */
export function ownedState(input: unknown, id: string): StateSnapshot {
  const value = parseContract<StateSnapshot>('StateSnapshot', input)
  if (value.project.id !== id || value.runtime.projectId !== id || stateNumber(value.replayFloor) > stateNumber(value.cursor)) throw invalidState()
  stateNumber(value.project.revision); ownedRuntime(value.runtime, value.project, value.graph)
  const members = new Set(value.project.nodes.map(node => node.id))
  if (members.size !== value.project.nodes.length || value.builds.length !== members.size || value.deployments.length !== members.size) throw invalidState()
  const builds = new Set<string>(); const deployments = new Set<string>()
  for (const build of value.builds) {
    if (builds.has(build.serviceId)) throw invalidState()
    ownedBuildState(build, value.project)
    builds.add(build.serviceId)
    stateNumber(build.generation); if (build.operation) stateNumber(build.operation.version)
  }
  for (const deployment of value.deployments) {
    ownedSnapshot(deployment, id, deployment.serviceId)
    if (!members.has(deployment.serviceId) || deployments.has(deployment.serviceId)) throw invalidState()
    deployments.add(deployment.serviceId)
    stateNumber(deployment.generation); stateNumber(deployment.sequence); if (deployment.operation) stateNumber(deployment.operation.version)
  }
  return value
}

/** @param value MySQL投影 @param project 已确认项目 @param graph 已确认组件 @returns 原目标、操作和资源必须属于同一项目与代次。 */
function ownedRuntime(value: RuntimeSnapshot, project: Project, graph: GraphState): void {
  stateNumber(value.generation); stateNumber(value.sequence)
  if (!value.target) {
    if (value.operation || value.observation || value.generation !== '0') throw invalidState()
    return
  }
  const { target, operation } = value
  if (target.projectId !== project.id || target.resource.projectId !== project.id || !graph.middlewares.some(node => node.id === target.resource.componentId) || target.generation !== value.generation || operation?.projectId !== project.id || operation.generation !== value.generation) throw invalidState()
  stateNumber(operation.version)
}

/** @param value 持久构建投影 @param project 当前服务登记 @returns 目标和操作交叉归属，历史候选保持独立记录。 */
function ownedBuildState(value: BuildSnapshot, project: Project): void {
  const node = project.nodes.find(node => node.id === value.serviceId)
  const source = value.currentSource
  if (value.projectId !== project.id || !node || source.projectId !== project.id || source.serviceId !== value.serviceId || source.projectRevision !== project.revision || source.repositoryId !== node.repository.id || source.repositoryPath !== node.repository.path) throw invalidState()
  if (!value.target) {
    if (value.operation || value.candidate || value.generation !== '0') throw invalidState()
    return
  }
  const { target, operation } = value
  if (target.projectId !== project.id || target.serviceId !== node.id || target.generation !== value.generation || target.source.projectId !== project.id || target.source.serviceId !== node.id || operation?.projectId !== project.id || operation.serviceId !== node.id || operation.targetId !== target.id) throw invalidState()
}

/** @param event SSE事件名 @param id 当前项目 @param wireId 原帧id @param data 未信任JSON @returns 有归属的业务或控制消息。 */
export function parseStateMessage(event: string, id: string, wireId: string | null, data: unknown): StateMessage {
  if (event === 'state') {
    const value = parseContract<StateEvent>('StateEvent', data)
    stateNumber(value.revision)
    if (value.projectId !== id || value.sequence !== wireId || value.id !== `${id}:${value.sequence}` || stateNumber(value.sequence) === 0n || (value.scope === 'project') !== (value.reference === null)) throw invalidState()
    if (value.reference) { stateNumber(value.reference.generation); stateNumber(value.reference.operationVersion); stateNumber(value.reference.execution) }
    if (value.reference && (value.scope === 'build' || value.scope === 'deployment') && !value.reference.serviceId) throw invalidState()
    return { kind: 'state', value }
  }
  if (wireId !== null) throw invalidState()
  if (event === 'caught_up') {
    const value = parseContract<StateCaughtUp>('StateCaughtUp', data)
    if (value.projectId !== id) throw invalidState()
    stateNumber(value.cursor)
    return { kind: 'caughtUp', value }
  }
  if (event !== 'resync') throw invalidState()
  const value = parseContract<StateResync>('StateResync', data)
  if (value.projectId !== id) throw invalidState()
  return { kind: 'resync', value }
}

/** @param current 同库候选与源码 @param checked 独立Git/Docker核实 @returns 只附着与当前投影精确匹配的外部可用性。 */
export function checkedBuild(current: BuildSnapshot, checked: BuildSnapshot): BuildSnapshot {
  const same = current.projectId === checked.projectId && current.serviceId === checked.serviceId && current.generation === checked.generation && current.operation?.id === checked.operation?.id && current.operation?.version === checked.operation?.version && current.candidate?.id === checked.candidate?.id && current.currentSource.repositoryId === checked.currentSource.repositoryId && current.currentSource.repositoryPath === checked.currentSource.repositoryPath && current.currentSource.pinnedCommit === checked.currentSource.pinnedCommit && current.currentSource.projectRevision === checked.currentSource.projectRevision
  return { ...current, candidateCurrent: same && checked.candidateCurrent, candidateCode: same ? checked.candidateCode : 'CURRENT_STATE_UNCONFIRMED' }
}
