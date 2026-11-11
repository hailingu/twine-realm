/** 生命周期来自同一 v6 信封，旧版本状态结构保持不变。 */
import type { ProjectRunSnapshot, StateSnapshot } from '../contract/generated'
import { parseContract } from '../project/api'
import { invalidState, ownedState, stateNumber } from '../projectstate/model'

/** 内部投影携带独立版本的父操作，不改变 v5 线协议。 */
export type LifecycleState = StateSnapshot & { lifecycle?: ProjectRunSnapshot }

/** @param input 已经读取的内部投影 @param id 当前项目 @returns 保留父状态且校验旧信封的投影。 */
export function ownedLifecycleState(input: LifecycleState, id: string): LifecycleState {
  const { lifecycle, ...state } = input
  const value = ownedState(state, id)
  if (!lifecycle) return value
  const life = parseContract<ProjectRunSnapshot>('ProjectRunSnapshot', lifecycle)
  if (life.projectId !== id) throw invalidState()
  stateNumber(life.generation)
  if (!life.target) {
    if (life.generation !== '0' || life.operation || life.facts) throw invalidState()
  } else if (life.target.projectId !== id || life.target.generation !== life.generation || life.operation?.projectId !== id || life.operation.generation !== life.generation || life.target.frozen.runtime.projectId !== id || life.target.frozen.deployment.projectId !== id) throw invalidState()
  return { ...value, lifecycle: life }
}
