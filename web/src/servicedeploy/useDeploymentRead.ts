/** 只读部署会话拥有轮询和迟到屏障，与确认命令的原键生命周期分开。 */
import { useEffect, useRef, useState } from 'react'
import type { BuildSnapshot, DeployCapability, DeploySnapshot } from '../contract/generated'
import type { BuildAPI } from '../servicebuild/api'
import { ownedSnapshot, type DeploymentAPI } from './api'
import { newerSnapshot } from './model'

/** @param api 部署查询 @param buildApi 候选查询 @param id 项目 @param serviceId 服务 @returns 最近一致投影与当前读取会话。 */
export function useDeploymentRead(api: DeploymentAPI | undefined, buildApi: BuildAPI | undefined, id: string, serviceId: string) {
  const [snapshot, setSnapshot] = useState<DeploySnapshot | null>(null)
  const [build, setBuild] = useState<BuildSnapshot | null>(null)
  const [capability, setCapability] = useState<DeployCapability | null>(null)
  const [readError, setReadError] = useState('')
  const lifetime = useRef<AbortController | null>(null)
  const refresh = useRef<(() => Promise<void>) | null>(null)
  useEffect(() => {
    if (!api || !buildApi) return
    const controller = new AbortController(); lifetime.current = controller
    let timer: number | undefined; let reading = false
    const poll = async () => {
      if (reading) return
      reading = true; if (timer !== undefined) window.clearTimeout(timer)
      try {
        const [cap, value, candidate] = await Promise.all([api.capability(controller.signal), api.snapshot(id, serviceId, controller.signal), buildApi.snapshot(id, serviceId, controller.signal)])
        ownedSnapshot(value, id, serviceId)
        if (candidate.projectId !== id || candidate.serviceId !== serviceId) throw new Error('构建候选归属不一致')
        if (!controller.signal.aborted) { setCapability(cap); setSnapshot(previous => newerSnapshot(previous, value)); setBuild(candidate); setReadError('') }
      } catch (error) { if (!controller.signal.aborted) setReadError(error instanceof Error ? error.message : '部署投影尚未确认，请稍后核实') }
      finally { reading = false; if (!controller.signal.aborted) timer = window.setTimeout(() => { void poll() }, 2000) }
    }
    refresh.current = poll; void poll()
    return () => { controller.abort(); refresh.current = null; if (timer !== undefined) window.clearTimeout(timer) }
  }, [api, buildApi, id, serviceId])
  return { snapshot, build, capability, readError, lifetime, refresh }
}
