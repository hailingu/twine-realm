/** 同一工作区共享只读信封；上下文不保存用户草稿、原请求或独立业务真相。 */
import { createContext, useContext } from 'react'
import type { LifecycleState as StateSnapshot } from '../projectrun/model'

/** 读取状态与外部命令寿命分离，错误不取消尚未确认的命令。 */
export interface StateProjection { snapshot: StateSnapshot | null; managed: boolean; error: string; now: number }

/** 同库投影由工作区唯一读取拥有者提供，独立组件测试可省略。 */
export const ProjectionContext = createContext<StateProjection | null>(null)

/** @returns 当前一致元数据投影，未装配时沿用显式旧读取端口。 */
export function useStateProjection(): StateProjection | null { return useContext(ProjectionContext) }
