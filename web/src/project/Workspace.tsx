/** 工作台展示明确保存状态，并为失败、未知结果和版本冲突提供恢复入口。 */
import { useMemo, useState } from 'react'
import type { Connection } from '@xyflow/react'
import type { CanvasPosition, CanvasState, GraphState, MiddlewareNode, Position, Project } from '../contract/generated'
import type { ProjectAPI } from './api'
import { Canvas } from './Canvas'
import { useWorkspace } from './useWorkspace'
import { SourceVersionPanel } from './SourceVersion'
import { MySQLPanel } from './MySQLPanel'
import type { RuntimeAPI } from '../runtime/api'
import { useRuntime } from '../runtime/useRuntime'
import { RuntimePanel } from '../runtime/RuntimePanel'
import { GraphInspector } from './GraphInspector'
import { bindingCandidate, type BindingCandidate } from './graphModel'
import type { BuildAPI } from '../servicebuild/api'
import { BuildPanel } from '../servicebuild/BuildPanel'
import { DeploymentPanel } from '../servicedeploy/DeploymentPanel'
import type { DeploymentAPI } from '../servicedeploy/api'
import type { StateAPI } from '../projectstate/api'
import { useProjectState } from '../projectstate/useProjectState'
import { ProjectionContext, useStateProjection } from '../projectstate/Projection'
import { StateNotice, RemoteStateNotice } from '../projectstate/StateNotice'

/** 独立状态读取可省略于组件测试，不影响已有命令边界。 */
interface WorkspaceProps { initial: Project; initialCanvas?: CanvasState; initialGraph?: GraphState; api: ProjectAPI; runtimeApi?: RuntimeAPI; buildApi?: BuildAPI; deploymentApi?: DeploymentAPI; stateApi?: StateAPI | null; onClose: () => void }

/** @param props 初始确认项目、HTTP 端口与退出命令 @returns 服务登记和布局工作区。 */
export function Workspace(props: Readonly<WorkspaceProps>) {
  const feed = useProjectState(props.stateApi, props.initial.id)
  const projection = useMemo(() => ({ managed: feed.managed, snapshot: feed.snapshot, error: feed.message, now: feed.now }), [feed.managed, feed.snapshot, feed.message, feed.now])
  return <ProjectionContext.Provider value={projection}><WorkspaceBody {...props} />{feed.managed && <StateNotice phase={feed.phase} cursor={feed.snapshot?.cursor} capturedAt={feed.snapshot?.capturedAt} message={feed.message} reconnect={feed.reconnect} />}</ProjectionContext.Provider>
}

/** @param props 编辑与命令端口 @returns 由独立读取投影驱动且保留本地候选的工作台。 */
function WorkspaceBody({ initial, initialCanvas, initialGraph, api, runtimeApi, buildApi, deploymentApi, onClose }: Readonly<WorkspaceProps>) {
  const projection = useStateProjection()
  const [path, setPath] = useState('')
  const [name, setName] = useState('')
  const [sourceId, setSourceId] = useState<string | null>(null)
  const [mysqlSelection, setMysqlSelection] = useState<string | null | false>(false)
  const [contractId, setContractId] = useState<string | null>(null)
  const [candidate, setCandidate] = useState<BindingCandidate | null>(null)
  const protectedForm = Boolean(path || name || sourceId || mysqlSelection !== false || contractId || candidate)
  const workspace = useWorkspace(initial, api, initialCanvas, initialGraph, projection?.snapshot, protectedForm)
  const runtime = useRuntime(runtimeApi, initial.id)
  const { snapshot, canvas, draft, dirty, blocked, saveState: save } = workspace
  const selected = snapshot.nodes.find(node => node.id === sourceId)
  const selectedMySQL = canvas.middlewares.find(node => node.id === mysqlSelection)
  const status = saveMessage(save.kind, dirty, snapshot.revision)
  const handleClose = () => { if (!blocked && (!dirty || window.confirm('当前布局尚未保存，确定放弃草稿并返回项目列表？'))) onClose() }
  const proposeBinding = (connection: Connection, bindingId?: string) => {
    if (blocked || dirty) return
    const source = connection.sourceHandle ? { componentId: connection.source, portId: connection.sourceHandle } : null
    const target = connection.targetHandle ? { componentId: connection.target, portId: connection.targetHandle } : null
    setCandidate(bindingCandidate(source, target, canvas.bindings.find(binding => binding.id === bindingId)))
  }
  return <main className="workspace"><div className="workspace-toolbar"><div className="project-title"><button type="button" className="back-button" onClick={handleClose} disabled={blocked} aria-label="返回项目列表">←</button><div><span className="eyebrow">PROJECT / LOCAL</span><h1>{snapshot.name}</h1></div></div><div className="save-actions"><output aria-label="画布保存状态" className={`save-status ${dirty || blocked ? 'unsaved' : ''}`}><i />{status}</output><button type="button" disabled={!dirty || blocked} onClick={workspace.discard}>还原布局</button><button type="button" className="primary" disabled={!dirty || blocked} onClick={workspace.save}>保存布局<span aria-hidden="true">↗</span></button></div></div>
    <div className="workspace-body"><Canvas project={snapshot} canvas={canvas} draft={draft} blocked={blocked} connectionBlocked={blocked || dirty} runtime={runtime.state} onMove={workspace.move} onConnect={proposeBinding} onReconnect={(id, connection) => { proposeBinding(connection, id) }} /><aside className="inspector"><button type="button" className="catalog-add" disabled={blocked || dirty} onClick={() => { setMysqlSelection(null) }}>＋ MySQL 组件</button><div className="inspector-title"><span className="eyebrow">ADD A SERVICE</span><h2>登记本地仓库</h2><p>已有代码，一个清晰的位置。</p></div>
      <form className="repository-form" onSubmit={event => { event.preventDefault(); workspace.register(path.trim(), name) }}><label htmlFor="repository-path">Git 工作区路径<span>必填</span></label><input id="repository-path" placeholder="/Users/you/projects/my-service" value={path} disabled={blocked || dirty} onChange={event => { setPath(event.target.value) }} required maxLength={4096} autoComplete="off" spellCheck={false} /><p className="field-help">使用启动时配置的允许目录内的绝对路径。</p><label htmlFor="service-name">服务显示名称<span>可选</span></label><input id="service-name" placeholder="默认使用仓库目录名" value={name} disabled={blocked || dirty} maxLength={80} onChange={event => { setName(event.target.value) }} /><button type="submit" className="primary full-width" disabled={blocked || dirty || !path.trim()}>＋ 加入画布</button>{dirty && <p className="field-help">先保存或还原布局，再加入新服务。</p>}</form>
      <div className="boundary-note"><span aria-hidden="true">◈</span><p>只读登记已有仓库。<br />节点保持待配置状态，仓库内容由你管理。</p></div>
      {save.kind === 'rejected' && <div className="notice error" role="alert"><strong>本次请求未保存</strong><p>{save.message}</p>{save.conflict && <button type="button" onClick={workspace.rebase}>载入最新版本并保留我的位置</button>}</div>}
      {save.kind === 'unknown' && <div className="recovery" role="alert"><strong>请先核实原请求</strong><p>{save.message}</p><button type="button" onClick={workspace.verify}>核实保存结果</button><button type="button" onClick={workspace.retry}>重试原请求</button><small>核实期间保留草稿与相同请求身份。</small></div>}
      {workspace.remote && <RemoteStateNotice remote={workspace.remote} blocked={blocked} apply={workspace.applyRemote} />}
      <CoordinateList project={snapshot} draft={draft} blocked={blocked} onMove={workspace.move} onSource={setSourceId} />
      <MiddlewareList nodes={canvas.middlewares} draft={draft} blocked={blocked} onMove={workspace.move} onSelect={setMysqlSelection} />
      {mysqlSelection !== false && <MySQLPanel key={mysqlSelection ?? 'new-mysql'} api={api} node={selectedMySQL} blocked={blocked || dirty} onSave={input => { workspace.middleware(selectedMySQL?.id ?? null, input) }} />}
      {selectedMySQL && <RuntimePanel key={`runtime-${selectedMySQL.id}`} projectId={snapshot.id} revision={snapshot.revision} node={selectedMySQL} runtime={runtime} api={runtimeApi} blocked={blocked || dirty} />}
      {selected && <SourceVersionPanel key={selected.id} projectId={snapshot.id} revision={snapshot.revision} node={selected} api={api} saved={workspace.sources[selected.id]} blocked={blocked} dirty={dirty} onPin={commit => { workspace.pin(selected.id, commit) }} />}
      {selected && <BuildPanel key={`build-${selected.id}`} api={buildApi} projectId={snapshot.id} serviceId={selected.id} revision={snapshot.revision} blocked={blocked || dirty} />}
      {selected && <DeploymentPanel key={`deployment-${selected.id}`} api={deploymentApi} buildApi={buildApi} projectId={snapshot.id} serviceId={selected.id} revision={snapshot.revision} bindings={canvas.bindings} blocked={blocked || dirty} />}
      <GraphInspector project={snapshot} graph={canvas} blocked={blocked || dirty} selectedId={contractId} candidate={candidate} onSelect={setContractId} onCandidate={setCandidate} onEdit={workspace.editGraph} />
      <footer className="inspector-foot">项目 ID <code>{snapshot.id}</code></footer>
    </aside></div>
  </main>
}

/** @param props 当前节点和位置草稿 @returns 键盘可用的画布位置编辑替代入口。 */
function CoordinateList({ project, draft, blocked, onMove, onSource }: Readonly<{ project: Project; draft: CanvasPosition[]; blocked: boolean; onMove: (id: string, position: Position) => void; onSource: (id: string) => void }>) {
  return <section className="coordinate-list" aria-label="服务位置"><div className="section-heading"><h3>画布上的服务</h3><span className="count">{project.nodes.length}</span></div>{project.nodes.length === 0 ? <p className="field-help">登记后可在此精确调整节点位置。</p> : project.nodes.map(node => {
    const position = draft.find(item => item.componentId === node.id) ?? node.position
    return <div className="coordinate-item" key={node.id}><strong>{node.displayName}</strong><small title={node.repository.path}>{node.repository.path}</small><div className="coordinate-inputs">{(['x', 'y'] as const).map(axis => <label key={axis}>{axis.toUpperCase()}<input aria-label={`${node.displayName} ${axis.toUpperCase()} ${node.id.slice(0, 8)}`} type="number" min={-1000000} max={1000000} step="any" disabled={blocked} value={position[axis]} onChange={event => { const value = event.target.valueAsNumber; if (Number.isFinite(value)) onMove(node.id, { x: position.x, y: position.y, [axis]: value }) }} /></label>)}</div><code>服务 {node.id.slice(0, 8)} · 仓库 {node.repository.id.slice(0, 8)}</code><button type="button" disabled={blocked} aria-label={`查看${node.displayName}源码版本`} onClick={() => { onSource(node.id) }}>源码版本</button></div>
  })}</section>
}

/** @param props 已保存中间件、位置草稿与显式编辑入口 @returns 可用键盘操作的统一位置列表。 */
function MiddlewareList({ nodes, draft, blocked, onMove, onSelect }: Readonly<{ nodes: MiddlewareNode[]; draft: CanvasPosition[]; blocked: boolean; onMove: (id: string, position: Position) => void; onSelect: (id: string) => void }>) {
  if (!nodes.length) return null
  return <section className="coordinate-list" aria-label="中间件位置"><h3>标准中间件</h3>{nodes.map(node => {
    const position = draft.find(item => item.componentId === node.id) ?? node.position
    return <div className="coordinate-item" key={node.id}><strong>{node.displayName}</strong><small>{node.definition.id} / {node.definition.version}</small><div className="coordinate-inputs">{(['x', 'y'] as const).map(axis => <label key={axis}>{axis.toUpperCase()}<input aria-label={`${node.displayName} ${axis.toUpperCase()} ${node.id.slice(0, 8)}`} type="number" min={-1000000} max={1000000} step="any" disabled={blocked} value={position[axis]} onChange={event => { const value = event.target.valueAsNumber; if (Number.isFinite(value)) onMove(node.id, { ...position, [axis]: value }) }} /></label>)}</div><button type="button" disabled={blocked} aria-label={`配置${node.displayName}`} onClick={() => { onSelect(node.id) }}>配置</button></div>
  })}</section>
}

/** @param kind 当前保存阶段 @param dirty 是否仍有位置草稿 @param revision 已确认版本 @returns 安全的用户可见保存说明。 */
function saveMessage(kind: string, dirty: boolean, revision: string): string {
  if (kind === "sending") return "正在确认保存…"
  if (kind === "unknown") return "结果尚未确认 · 草稿已保留"
  if (dirty) return "布局有未保存的修改"
  return `已保存到本机 · 版本 ${revision}`
}
