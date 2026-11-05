/** 工作区会话拥有确认快照、可见草稿和原请求，卸载后旧结果不再写入状态。 */
import { useEffect, useReducer, useRef } from 'react'
import type { Position, Project, Receipt, SourceReceipt, SourceVersion } from '../contract/generated'
import { RequestError, type ProjectAPI } from './api'
import { isDirty, movePosition, positionsOf, rebasePositions, type PendingRequest, type SaveState } from './model'

interface WorkspaceState { snapshot: Project; draft: ReturnType<typeof positionsOf>; save: SaveState; sources: Record<string, SourceVersion> }
type Action = { kind: 'accepted'; value: Project; source: SourceVersion | null } | { kind: 'save'; value: SaveState } | { kind: 'move'; id: string; position: Position } | { kind: 'rebase'; value: Project } | { kind: 'discard' }

/** @param state 会话状态 @param action 明确状态转换 @returns 不可变的新状态。 */
function reduce(state: WorkspaceState, action: Action): WorkspaceState {
  switch (action.kind) {
    case 'accepted': return { snapshot: action.value, draft: positionsOf(action.value), save: { kind: 'ready' }, sources: action.source ? { ...state.sources, [action.source.serviceId]: action.source } : state.sources }
    case 'save': return { ...state, save: action.value }
    case 'move': return { ...state, draft: movePosition(state.draft, action.id, action.position) }
    case 'rebase': return { ...state, snapshot: action.value, draft: rebasePositions(action.value.nodes, state.draft), save: { kind: 'ready' } }
    case 'discard': return { ...state, draft: positionsOf(state.snapshot), save: { kind: 'ready' } }
  }
}

/** @param api 副作用端口 @param pending 原请求 @param signal 会话生命周期 @returns 原请求的确认回执。 */
async function perform(api: ProjectAPI, pending: PendingRequest, signal: AbortSignal): Promise<Receipt | SourceReceipt> {
  if (pending.kind === 'pinSource') return api.pin(pending.projectId, pending.serviceId, pending.command, signal)
  return pending.kind === 'saveLayout' ? api.layout(pending.projectId, pending.command, signal) : api.register(pending.projectId, pending.command, signal)
}

/** @param api 读取端口 @param pending 原命令 @param signal 会话 @returns 与命令类型一致的专用核实结果。 */
async function verifyRequest(api: ProjectAPI, pending: PendingRequest, signal: AbortSignal): Promise<Receipt | SourceReceipt> {
  if (pending.kind === 'pinSource') return api.sourceReceipt(pending.command.mutationId, signal)
  return api.receipt(pending.command.mutationId, signal)
}

/** @param receipt 已通过机器 schema 的回执 @param request 原请求 @returns 身份及源码选择不一致时拒绝确认。 */
function verifyIdentity(receipt: Receipt | SourceReceipt, request: PendingRequest): void {
  if (receipt.mutationId !== request.command.mutationId || receipt.kind !== request.kind || receipt.project.id !== request.projectId) throw new RequestError({ code: 'INVALID_RESPONSE', message: '回执身份不匹配，请核实原请求' }, 0)
  if (request.kind === 'pinSource' && (receipt.kind !== 'pinSource' || receipt.sourceVersion.serviceId !== request.serviceId || receipt.sourceVersion.pinnedCommit !== request.command.commit)) throw new RequestError({ code: 'INVALID_RESPONSE', message: '源码回执与待确认选择不匹配，请核实原请求' }, 0)
}

/** @param initial 首次确认快照 @param api HTTP 端口 @returns 当前会话和同步事件入口。 */
export function useWorkspace(initial: Project, api: ProjectAPI) {
  const [state, dispatch] = useReducer(reduce, { snapshot: initial, draft: positionsOf(initial), save: { kind: 'ready' }, sources: {} })
  const session = useRef(new AbortController())
  const inFlight = useRef(false)
  useEffect(() => { const controller = new AbortController(); session.current = controller; return () => { controller.abort() } }, [])
  const dirty = isDirty(state.snapshot, state.draft)
  const blocked = state.save.kind === 'sending' || state.save.kind === 'unknown'
  useEffect(() => {
    if (!dirty && !blocked) return
    const handleUnload = (event: BeforeUnloadEvent) => { event.preventDefault() }
    window.addEventListener('beforeunload', handleUnload)
    return () => { window.removeEventListener('beforeunload', handleUnload) }
  }, [dirty, blocked])
  const run = async (request: PendingRequest, verify = false) => {
    if (inFlight.current) return
    inFlight.current = true
    const controller = session.current
    dispatch({ kind: 'save', value: { kind: 'sending', request } })
    try {
      const receipt = verify ? await verifyRequest(api, request, controller.signal) : await perform(api, request, controller.signal)
      verifyIdentity(receipt, request)
      if (!controller.signal.aborted) dispatch({ kind: 'accepted', value: receipt.project, source: receipt.kind === 'pinSource' ? receipt.sourceVersion : null })
    } catch (error) {
      if (controller.signal.aborted) return
      const failure = error instanceof RequestError ? error : new RequestError({ code: 'UNEXPECTED_FAILURE', message: '请求结果尚未确认，请核实或重试原请求' }, 0)
      const uncertain = verify || failure.status === 0 || failure.status >= 500
      dispatch({ kind: 'save', value: uncertain ? { kind: 'unknown', request, message: failure.message } : { kind: 'rejected', message: failure.message, conflict: failure.failure.code === 'REVISION_CONFLICT' } })
    } finally { inFlight.current = false }
  }
  const recover = async () => {
    if (inFlight.current) return
    inFlight.current = true
    const controller = session.current
    try { const latest = await api.get(state.snapshot.id, controller.signal); if (!controller.signal.aborted) dispatch({ kind: 'rebase', value: latest }) }
    catch (error) { if (!controller.signal.aborted) dispatch({ kind: 'save', value: { kind: 'rejected', message: error instanceof Error ? error.message : '读取最新版本失败', conflict: true } }) }
    finally { inFlight.current = false }
  }
  return { ...state, saveState: state.save, dirty, blocked,
    move: (id: string, position: Position) => { if (!blocked) dispatch({ kind: 'move', id, position }) },
    register: (path: string, displayName: string) => { if (!blocked && !dirty) void run({ kind: 'registerRepository', projectId: state.snapshot.id, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, repositoryPath: path, ...(displayName.trim() ? { displayName: displayName.trim() } : {}), position: { x: 100 + state.snapshot.nodes.length * 36, y: 100 + state.snapshot.nodes.length * 36 } } }) },
    save: () => { if (!blocked && dirty) void run({ kind: 'saveLayout', projectId: state.snapshot.id, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, positions: state.draft } }) },
    pin: (serviceId: string, commit: string) => { if (!blocked && !dirty) void run({ kind: 'pinSource', projectId: state.snapshot.id, serviceId, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, commit } }) },
    retry: () => { if (state.save.kind === 'unknown') void run(state.save.request) },
    verify: () => { if (state.save.kind === 'unknown') void run(state.save.request, true) },
    rebase: () => { if (!blocked) void recover() },
    discard: () => { if (!blocked) dispatch({ kind: 'discard' }) }
  }
}
