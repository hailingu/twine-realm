/** 工作区拥有持久运行的轮询与原请求恢复，所有异步完成都检查会话和请求归属。 */
import { useEffect, useReducer, useRef } from 'react'
import type { RuntimeCommand, RuntimeReceipt } from '../contract/generated'
import { RequestError } from '../project/api'
import type { RuntimeAPI } from './api'
import { initialRuntime, runtimeReducer } from './model'
import { useStateProjection } from '../projectstate/Projection'

/** 客户端只保存原路由和命令；后台操作由服务端持久拥有。 */
interface Pending { componentId: string; command: RuntimeCommand }

/** @param error 边界错误 @returns 适合直接呈现且不包含原始响应的说明。 */
function message(error: unknown): string { return error instanceof RequestError ? error.failure.message : '运行请求未得到可确认结果，请保留原请求并核实' }

/** @param error 边界错误 @returns 是否能确定目标没有被接受。 */
function rejected(error: unknown): boolean { return error instanceof RequestError && ((error.status >= 400 && error.status < 500) || error.failure.code === 'RUNTIME_DISABLED') && error.failure.code !== 'RESULT_UNKNOWN' }

/** @param api 可选的独立运行端口 @param projectId 会话项目 @returns 同一份运行投影和同步事件入口。 */
export function useRuntime(api: RuntimeAPI | undefined, projectId: string) {
  const projection = useStateProjection()
  const managed = projection?.managed === true
  const projected = projection?.snapshot
  const [state, dispatch] = useReducer(runtimeReducer, undefined, initialRuntime)
  const lifetime = useRef<AbortController | null>(null)
  const active = useRef<Pending | null>(null)
  const inFlight = useRef(false)
  useEffect(() => {
    if (!api) return
    const controller = new AbortController()
    lifetime.current = controller
    let timer: number | undefined
    const poll = async () => {
      try {
        const [capability, value] = await Promise.all([api.capability(controller.signal), managed ? Promise.resolve(null) : api.snapshot(projectId, controller.signal)])
        if (!controller.signal.aborted) {
          if (value && value.projectId !== projectId) throw new Error('项目投影归属不一致')
          dispatch({ type: 'capability', value: capability }); if (value) dispatch({ type: 'snapshot', value })
        }
      } catch (error) { if (!controller.signal.aborted) dispatch({ type: 'readFailed', message: message(error) }) }
      finally { if (!controller.signal.aborted) { dispatch({ type: 'clock', now: Date.now() }); timer = window.setTimeout(() => { void poll() }, 2000) } }
    }
    // poll 自己持有拒绝通道和计时器，卸载只终止客户端读取，不发送取消或停止目标。
    void poll()
    return () => { controller.abort(); if (timer !== undefined) window.clearTimeout(timer) }
  }, [api, projectId, managed])
  useEffect(() => { if (managed && projected?.project.id === projectId) dispatch({ type: 'snapshot', value: projected.runtime }) }, [managed, projected, projectId])
  useEffect(() => {
    const id = active.current?.command.mutationId
    if (state.submission.kind === 'rejected' && state.submission.id === id) active.current = null
    if (state.submission.kind === 'idle' && state.lastReceipt?.mutationId === id) active.current = null
  }, [state.submission, state.lastReceipt])

  const acceptReceipt = (pending: Pending, receipt: RuntimeReceipt) => {
    if (receipt.target.projectId !== projectId || receipt.target.resource.projectId !== projectId || receipt.target.resource.componentId !== pending.componentId || receipt.mutationId !== pending.command.mutationId || receipt.target.canvasRevision !== pending.command.baseRevision || receipt.target.desired !== pending.command.desired || BigInt(receipt.target.generation) !== BigInt(pending.command.baseGeneration) + 1n) throw new RequestError({ code: 'INVALID_RESPONSE', message: '原运行回执与请求归属不一致，请继续核实' }, 0)
    dispatch({ type: 'accepted', id: pending.command.mutationId, receipt })
  }
  const send = async (pending: Pending) => {
    const signal = lifetime.current?.signal
    if (!api || !signal || signal.aborted || inFlight.current) return
    inFlight.current = true
    dispatch({ type: 'sending', command: pending.command })
    try { const receipt = await api.accept(projectId, pending.componentId, pending.command, signal); if (!signal.aborted) acceptReceipt(pending, receipt) }
    catch (error) { if (!signal.aborted) dispatch(rejected(error) ? { type: 'rejected', id: pending.command.mutationId, message: message(error) } : { type: 'unknown', id: pending.command.mutationId, message: message(error) }) }
    finally { if (lifetime.current?.signal === signal) inFlight.current = false }
  }
  const verify = async () => {
    const pending = active.current; const signal = lifetime.current?.signal
    if (!api || !pending || !signal || signal.aborted || inFlight.current) return
    inFlight.current = true; dispatch({ type: 'verifying', id: pending.command.mutationId })
    try { const receipt = await api.receipt(pending.command.mutationId, signal); if (!signal.aborted) acceptReceipt(pending, receipt) }
    catch (error) { if (!signal.aborted) dispatch({ type: 'unknown', id: pending.command.mutationId, message: message(error) }) }
    finally { if (lifetime.current?.signal === signal) inFlight.current = false }
  }
  return {
    state: managed ? { ...state, now: projection.now, readError: projection.error || state.readError } : state,
    run(componentId: string, revision: string, desired: 'running' | 'stopped') {
      if (!api || active.current || !state.snapshot || !state.capability?.enabled) return
      const command: RuntimeCommand = { mutationId: crypto.randomUUID(), baseRevision: revision, baseGeneration: state.snapshot.generation, desired, ...(state.snapshot.operation ? { retryOf: state.snapshot.operation.id } : {}) }
      const pending = { componentId, command }; active.current = pending
      // send 自己处理成功、拒绝和未知，事件入口保持同步。
      void send(pending)
    },
    retry() { const pending = active.current; if (pending && state.submission.kind === 'unknown') void send(pending) },
    verify() { if (state.submission.kind === 'unknown') void verify() }
  }
}
