/** 独立构建会话拥有轮询和原键恢复，卸载只取消客户端请求。 */
import { useEffect, useReducer, useRef } from 'react'
import type { BuildIntent } from './model'
import { buildReducer, initialBuild } from './model'
import { intentReceipt, type BuildAPI } from './api'
import { RequestError } from '../project/api'

/** @param error 请求边界错误 @returns 不含原工具输出的用户说明。 */
function message(error: unknown): string { return error instanceof Error ? error.message : '构建请求结果尚未确认，请保留原请求并核实' }
/** @param error 边界错误 @returns 是否明确没有受理；提交未知不得换键。 */
function rejected(error: unknown): boolean { return error instanceof RequestError && error.status >= 400 && error.status < 500 && error.failure.code !== 'RESULT_UNKNOWN' || error instanceof RequestError && error.failure.code === 'BUILD_DISABLED' }

/** @param api 受控端口 @param projectId 项目会话 @param serviceId 服务会话 @param revision 已保存画布版本 @returns 构建状态与同步命令入口。 */
export function useBuild(api: BuildAPI | undefined, projectId: string, serviceId: string, revision: string) {
  const [state, dispatch] = useReducer(buildReducer, undefined, initialBuild)
  const lifetime = useRef<AbortController | null>(null)
  const active = useRef<BuildIntent | null>(null)
  const inFlight = useRef(false)
  const refresh = useRef<(() => Promise<void>) | null>(null)
  useEffect(() => {
    if (!api) return
    const controller = new AbortController(); lifetime.current = controller
    let timer: number | undefined
    let reading = false
    const poll = async () => {
      if (reading) return
      reading = true
      if (timer !== undefined) window.clearTimeout(timer)
      try {
        const [capability, value] = await Promise.all([api.capability(controller.signal), api.snapshot(projectId, serviceId, controller.signal)])
        if (!controller.signal.aborted) {
          if (value.projectId !== projectId || value.serviceId !== serviceId) { throw new Error('构建投影归属不一致') }
          dispatch({ type: 'capability', value: capability })
          dispatch({ type: 'snapshot', value })
        }
      } catch (error) { if (!controller.signal.aborted) dispatch({ type: 'readFailed', message: message(error) }) }
      finally { reading = false; if (!controller.signal.aborted) timer = window.setTimeout(() => { void poll() }, 2000) }
    }
    refresh.current = poll
    void poll()
    return () => { controller.abort(); refresh.current = null; if (timer !== undefined) window.clearTimeout(timer) }
  }, [api, projectId, serviceId])
  useEffect(() => { if (refresh.current) void refresh.current() }, [revision])

  const failIntent = (error: unknown, verify: boolean, intent: BuildIntent, signal: AbortSignal) => {
    if (signal.aborted) return
    const definitive = !verify && rejected(error)
    dispatch({ type: definitive ? 'rejected' : 'unknown', id: intent.command.mutationId, message: message(error) })
    if (definitive) active.current = null
  }
  const submit = async (intent: BuildIntent, verify: boolean) => {
    const signal = lifetime.current?.signal
    if (!api || !signal || signal.aborted || inFlight.current) return
    inFlight.current = true; dispatch({ type: 'sending', intent })
    try {
      const value = await performIntent(api, projectId, serviceId, intent, signal, verify)
      const receipt = intentReceipt(value, projectId, serviceId, intent)
      if (!signal.aborted) { dispatch({ type: 'accepted', id: intent.command.mutationId, receipt }); active.current = null; if (refresh.current) void refresh.current() }
    } catch (error) { failIntent(error, verify, intent, signal) }
    finally { if (lifetime.current?.signal === signal) inFlight.current = false }
  }
  const begin = (intent: BuildIntent) => {
    if (active.current) return
    active.current = intent
    void submit(intent, false)
  }
  return {
    state,
    build() {
      const value = state.snapshot; const capability = state.capability
      if (!value?.currentSource.pinnedCommit || !capability?.enabled || state.readError || value.currentSource.projectRevision !== revision) return
      begin({ kind: 'build', command: { mutationId: crypto.randomUUID(), baseRevision: revision, baseGeneration: value.generation, commit: value.currentSource.pinnedCommit, profile: capability.profile, ...(value.operation ? { retryOf: value.operation.id } : {}) } })
    },
    cancel() { const operation = state.snapshot?.operation; if (operation) begin({ kind: 'cancelBuild', operationId: operation.id, command: { mutationId: crypto.randomUUID(), baseVersion: operation.version } }) },
    retry() { if (active.current && state.submission.kind === 'unknown') void submit(active.current, false) },
    verify() { if (active.current && state.submission.kind === 'unknown') void submit(active.current, true) }
  }
}

/** @param api 受控端口 @param projectId 项目 @param serviceId 服务 @param intent 原意图 @param signal 会话期限 @param verify 是否仅查询原键 @returns 受理或原键查询结果，尚需归属核实。 */
async function performIntent(api: BuildAPI, projectId: string, serviceId: string, intent: BuildIntent, signal: AbortSignal, verify: boolean) {
  if (verify) return api.receipt(intent.command.mutationId, signal)
  if (intent.kind === 'build') return api.accept(projectId, serviceId, intent.command, signal)
  return api.cancel(projectId, serviceId, intent.operationId, intent.command, signal)
}
