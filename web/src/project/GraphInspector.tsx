/** 图检查器把手势候选和确认关系分开，失败、重连与删除均通过同一原子入口。 */
import type { DatabaseBinding, GraphOperation, GraphState, Project } from '../contract/generated'
import { BindingPanel } from './BindingPanel'
import { ServiceContractPanel } from './ServiceContractPanel'
import { bindingCandidate, type BindingCandidate } from './graphModel'

/** @param props 领域确认状态与可替换候选 @returns 可用键盘审阅和编辑的契约 / 依赖入口。 */
export function GraphInspector({ project, graph, blocked, selectedId, candidate, onSelect, onCandidate, onEdit }: Readonly<{ project: Project; graph: GraphState; blocked: boolean; selectedId: string | null; candidate: BindingCandidate | null; onSelect: (id: string) => void; onCandidate: (candidate: BindingCandidate) => void; onEdit: (...operations: [GraphOperation, ...GraphOperation[]]) => void }>) {
  const selected = project.nodes.find(node => node.id === selectedId)
  return <section className="graph-inspector" aria-label="接口与依赖"><h3>接口与依赖声明</h3><p className="field-help">具名端口来自明确版本的服务契约；未定义的服务保持草稿。连线方向为消费服务 → 提供 MySQL。</p>
    <div className="contract-actions">{project.nodes.map(node => <button type="button" key={node.id} disabled={blocked} onClick={() => { onSelect(node.id) }} aria-label={`编辑${node.displayName}接口契约`}>{node.displayName} · 接口契约</button>)}</div>
    {selected && <ServiceContractPanel key={selected.id} node={selected} graph={graph} blocked={blocked} onEdit={onEdit} />}
    <button type="button" disabled={blocked} onClick={() => { onCandidate(bindingCandidate()) }}>建立数据库依赖</button>
    <BindingList bindings={graph.bindings} blocked={blocked} onCandidate={onCandidate} onEdit={onEdit} />
    {candidate && <BindingPanel key={candidate.key} project={project} graph={graph} candidate={candidate} blocked={blocked} onEdit={onEdit} />}
  </section>
}

/** @param props 已确认关系与明确动作 @returns 不把移除声明解释为删除数据库的关系列表。 */
function BindingList({ bindings, blocked, onCandidate, onEdit }: Readonly<{ bindings: DatabaseBinding[]; blocked: boolean; onCandidate: (candidate: BindingCandidate) => void; onEdit: (...operations: [GraphOperation, ...GraphOperation[]]) => void }>) {
  return <section aria-label="已保存数据库依赖">{bindings.length ? bindings.map(binding => <div className="binding-item" key={binding.id}>
    <strong>{binding.databaseName}</strong><span>{binding.source.portId} → {binding.target.portId} · {binding.purpose === 'readOnly' ? '应用只读' : '应用读写'}</span><small>仅声明 · 尚未应用</small><code>绑定 {binding.id}</code>
    <button type="button" disabled={blocked} aria-label={`重连${binding.databaseName}`} onClick={() => { onCandidate(bindingCandidate(null, null, binding)) }}>重连</button>
    <button type="button" disabled={blocked} aria-label={`移除${binding.databaseName}依赖声明`} onClick={() => { if (window.confirm(`移除 ${binding.databaseName} 的依赖声明？这不会删除数据库或数据。`)) onEdit({ type: 'binding.remove', bindingId: binding.id }) }}>移除声明</button>
  </div>) : <p className="field-help">尚无已保存的依赖声明。</p>}</section>
}
