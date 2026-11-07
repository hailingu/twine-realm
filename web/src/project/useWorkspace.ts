/** 工作区会话拥有确认快照、可见草稿和原请求，卸载后旧结果不再写入状态。 */
import { useEffect, useReducer, useRef } from 'react'
import type { CanvasReceipt, CanvasState, GraphOperation, GraphReceipt, GraphSnapshot, GraphState, MiddlewareCommand, Position, Project, Receipt, SourceReceipt, SourceVersion } from '../contract/generated'
import { RequestError, type ProjectAPI } from './api'
import { isDirty, movePosition, positionsOf, rebasePositions, type PendingRequest, type SaveState } from './model'

interface WorkspaceState { snapshot: Project; canvas: GraphState; draft: ReturnType<typeof positionsOf>; save: SaveState; sources: Record<string, SourceVersion>; remote: GraphSnapshot | null }
type Action = { kind: 'accepted'; value: Project; source: SourceVersion | null; canvas: CanvasState | null; graph: GraphState | null } | { kind: 'save'; value: SaveState } | { kind: 'move'; id: string; position: Position } | { kind: 'rebase'; value: GraphSnapshot } | { kind: 'discard' } | { kind: 'external'; value: GraphSnapshot; protect: boolean } | { kind: 'applyRemote' }
type AcceptedReceipt = Receipt | SourceReceipt | CanvasReceipt | GraphReceipt
/** 缺省集合只参与不可变状态初始化，不在参数默认表达式中创建可变对象。 */
const EMPTY_GRAPH: GraphState = { schemaVersion: 2, middlewares: [], serviceContracts: [], bindings: [] }

/** @param previous 原确认图 @param canvas 未涉及连接的旧类回执 @param graph 完整新图 @returns 基准版本保护下的单一确认状态。 */
function confirmedGraph(previous: GraphState, canvas: CanvasState | null, graph: GraphState | null): GraphState {
  if (graph) return graph
  return canvas ? { ...previous, middlewares: canvas.middlewares } : previous
}

/** @param state 会话状态 @param action 明确状态转换 @returns 不可变的新状态。 */
function reduce(state: WorkspaceState, action: Action): WorkspaceState {
  switch (action.kind) {
    case 'accepted': {
      if (BigInt(action.value.revision) < BigInt(state.snapshot.revision)) return { ...state, save: { kind: 'ready' } }
      const graph = confirmedGraph(state.canvas, action.canvas, action.graph)
      return { ...state, snapshot: action.value, canvas: graph, draft: positionsOf(action.value, graph), save: { kind: 'ready' }, sources: action.source ? { ...state.sources, [action.source.serviceId]: action.source } : state.sources, remote: state.remote && BigInt(state.remote.project.revision) > BigInt(action.value.revision) ? state.remote : null }
    }
    case 'save': return { ...state, save: action.value }
    case 'move': return { ...state, draft: movePosition(state.draft, action.id, action.position) }
    case 'rebase': return BigInt(action.value.project.revision) < BigInt(state.snapshot.revision) ? state : { ...state, snapshot: action.value.project, canvas: action.value.graph, draft: changedDraft(state, action.value), save: { kind: 'ready' }, remote: null, sources: {} }
    case 'discard': return { ...state, draft: positionsOf(state.snapshot, state.canvas), save: { kind: 'ready' } }
    case 'external': return receiveExternal(state, action.value, action.protect)
    case 'applyRemote': return state.remote && state.save.kind !== 'sending' && state.save.kind !== 'unknown' ? { ...state, snapshot: state.remote.project, canvas: state.remote.graph, draft: changedDraft(state, state.remote), remote: null, sources: {} } : state
  }
}

/** @param state 原确认位置和交互草稿 @param value 新确认图 @returns 只保留实际改动位置，其余节点采用远端位置。 */
function changedDraft(state: WorkspaceState, value: GraphSnapshot): ReturnType<typeof positionsOf> {
  const saved = positionsOf(state.snapshot, state.canvas)
  const changes = state.draft.filter(item => { const original = saved.find(position => position.componentId === item.componentId); return original && (original.x !== item.x || original.y !== item.y) })
  return rebasePositions([...value.project.nodes, ...value.graph.middlewares], changes)
}

/** @param state 原草稿与确认状态 @param value 远端一致图 @param protect 是否有打开的表单 @returns 不偷偷推进旧表单基准版本的状态。 */
function receiveExternal(state: WorkspaceState, value: GraphSnapshot, protect: boolean): WorkspaceState {
  if (value.project.id !== state.snapshot.id || BigInt(value.project.revision) <= BigInt(state.snapshot.revision)) return state
  if (protect || isDirty(state.snapshot, state.draft, state.canvas) || state.save.kind === 'sending' || state.save.kind === 'unknown') {
    return state.remote && BigInt(state.remote.project.revision) >= BigInt(value.project.revision) ? state : { ...state, remote: value }
  }
  return { ...state, snapshot: value.project, canvas: value.graph, draft: positionsOf(value.project, value.graph), remote: null, sources: {} }
}

/** @param api 副作用端口 @param pending 原请求 @param signal 会话生命周期 @returns 原请求的确认回执。 */
async function perform(api: ProjectAPI, pending: PendingRequest, signal: AbortSignal): Promise<AcceptedReceipt> {
  switch (pending.kind) {
    case 'pinSource': return api.pin(pending.projectId, pending.serviceId, pending.command, signal)
    case 'registerRepository': return api.register(pending.projectId, pending.command, signal)
    case 'saveCanvasLayout': return api.layout(pending.projectId, pending.command, signal)
    case 'addMiddleware': return api.addMiddleware(pending.projectId, pending.command, signal)
    case 'updateMiddleware': return api.updateMiddleware(pending.projectId, pending.componentId, pending.command, signal)
    case 'editGraph': return api.editGraph(pending.projectId, pending.command, signal)
  }
}

/** @param api 读取端口 @param pending 原命令 @param signal 会话 @returns 与命令类型一致的专用核实结果。 */
async function verifyRequest(api: ProjectAPI, pending: PendingRequest, signal: AbortSignal): Promise<AcceptedReceipt> {
  if (pending.kind === 'pinSource') return api.sourceReceipt(pending.command.mutationId, signal)
  if (pending.kind === 'editGraph') return api.graphReceipt(pending.command.mutationId, signal)
  if (pending.kind === 'registerRepository') return api.receipt(pending.command.mutationId, signal)
  return api.canvasReceipt(pending.command.mutationId, signal)
}

/** @param receipt 已通过机器 schema 的回执 @param request 原请求 @returns 身份及源码选择不一致时拒绝确认。 */
function verifyIdentity(receipt: AcceptedReceipt, request: PendingRequest): void {
  if (receipt.mutationId !== request.command.mutationId || receipt.kind !== request.kind || receipt.project.id !== request.projectId) throw new RequestError({ code: 'INVALID_RESPONSE', message: '回执身份不匹配，请核实原请求' }, 0)
  if (request.kind === 'pinSource' && (receipt.kind !== 'pinSource' || receipt.sourceVersion.serviceId !== request.serviceId || receipt.sourceVersion.pinnedCommit !== request.command.commit)) throw new RequestError({ code: 'INVALID_RESPONSE', message: '源码回执与待确认选择不匹配，请核实原请求' }, 0)
}

/** @param error 外部失败 @param request 原意图 @param verify 是否正在核实 @returns 保留未知结果身份且显式区分可纠正拒绝的状态。 */
function failedSave(error: unknown, request: PendingRequest, verify: boolean): SaveState {
  const failure = error instanceof RequestError ? error : new RequestError({ code: 'UNEXPECTED_FAILURE', message: '请求结果尚未确认，请核实或重试原请求' }, 0)
  const uncertain = verify || failure.status === 0 || failure.status >= 500
  return uncertain ? { kind: 'unknown', request, message: failure.message } : { kind: 'rejected', message: failure.message, conflict: failure.failure.code === 'REVISION_CONFLICT' }
}

/** @param initial 首次确认快照 @param api HTTP 端口 @returns 当前会话和同步事件入口。 */
export function useWorkspace(initial: Project, api: ProjectAPI, initialCanvas?: CanvasState, initialGraph?: GraphState, external?: GraphSnapshot | null, protect = false) {
  const graph = initialGraph ?? { ...EMPTY_GRAPH, middlewares: initialCanvas?.middlewares ?? EMPTY_GRAPH.middlewares }
  const [state, dispatch] = useReducer(reduce, { snapshot: initial, canvas: graph, draft: positionsOf(initial, graph), save: { kind: 'ready' }, sources: {}, remote: null })
  const session = useRef(new AbortController())
  const inFlight = useRef(false)
  useEffect(() => { const controller = new AbortController(); session.current = controller; return () => { controller.abort() } }, [])
  const dirty = isDirty(state.snapshot, state.draft, state.canvas)
  const blocked = state.save.kind === 'sending' || state.save.kind === 'unknown'
  useEffect(() => { if (external) dispatch({ kind: 'external', value: external, protect }) }, [external, protect, dirty, blocked, state.snapshot.revision])
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
      if (!controller.signal.aborted) dispatch({ kind: 'accepted', value: receipt.project, source: receipt.kind === 'pinSource' ? receipt.sourceVersion : null, canvas: 'canvas' in receipt ? receipt.canvas : null, graph: 'graph' in receipt ? receipt.graph : null })
    } catch (error) {
      if (controller.signal.aborted) return
      dispatch({ kind: 'save', value: failedSave(error, request, verify) })
    } finally { inFlight.current = false }
  }
  const recover = async () => {
    if (inFlight.current) return
    inFlight.current = true
    const controller = session.current
    try { const latest = await api.graph(state.snapshot.id, controller.signal); if (!controller.signal.aborted) dispatch({ kind: 'rebase', value: latest }) }
    catch (error) { if (!controller.signal.aborted) dispatch({ kind: 'save', value: { kind: 'rejected', message: error instanceof Error ? error.message : '读取最新版本失败', conflict: true } }) }
    finally { inFlight.current = false }
  }
  return { ...state, saveState: state.save, dirty, blocked,
    move: (id: string, position: Position) => { if (!blocked) dispatch({ kind: 'move', id, position }) },
    register: (path: string, displayName: string) => { if (!blocked && !dirty) void run({ kind: 'registerRepository', projectId: state.snapshot.id, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, repositoryPath: path, ...(displayName.trim() ? { displayName: displayName.trim() } : {}), position: { x: 100 + state.snapshot.nodes.length * 36, y: 100 + state.snapshot.nodes.length * 36 } } }) },
    save: () => { if (!blocked && dirty) void run({ kind: 'saveCanvasLayout', projectId: state.snapshot.id, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, positions: state.draft } }) },
    pin: (serviceId: string, commit: string) => { if (!blocked && !dirty) void run({ kind: 'pinSource', projectId: state.snapshot.id, serviceId, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, commit } }) },
    editGraph: (...operations: [GraphOperation, ...GraphOperation[]]) => { if (!blocked && !dirty) void run({ kind: 'editGraph', projectId: state.snapshot.id, command: { mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision, operations } }) },
    middleware: (componentId: string | null, input: Omit<MiddlewareCommand, 'mutationId' | 'baseRevision'>) => {
      if (blocked || dirty) return
      const command = { ...input, mutationId: crypto.randomUUID(), baseRevision: state.snapshot.revision }
      if (componentId) void run({ kind: 'updateMiddleware', projectId: state.snapshot.id, componentId, command })
      else void run({ kind: 'addMiddleware', projectId: state.snapshot.id, command })
    },
    retry: () => { if (state.save.kind === 'unknown') void run(state.save.request) },
    verify: () => { if (state.save.kind === 'unknown') void run(state.save.request, true) },
    rebase: () => { if (!blocked) void recover() },
    applyRemote: () => { if (!blocked) dispatch({ kind: 'applyRemote' }) },
    discard: () => { if (!blocked) dispatch({ kind: 'discard' }) }
  }
}
