/** 服务契约编辑只发布声明；自定义 schema 不在浏览器动态编译或执行。 */
import { useState } from 'react'
import type { DatabaseBinding, GraphOperation, GraphState, ServiceContractDocument, ServiceNode } from '../contract/generated'
import { SERVICE_CONTRACT_EXAMPLE } from '../contract/graph.generated'
import { parseContract } from './api'

/** @param text 用户候选 @returns 结构校验结果；真正的 schema / 引用编译由服务端裁决。 */
function readDocument(text: string): { value: ServiceContractDocument | null; error: string | null } {
  if (!text.trim()) return { value: null, error: '请填写契约，或显式载入端口示例。' }
  try { const value: unknown = JSON.parse(text); return { value: parseContract<ServiceContractDocument>('ServiceContractDocument', value), error: null } }
  catch { return { value: null, error: '契约 JSON 或标识、版本、接口 / 端口结构无效。' } }
}

/** @param props 当前服务、确认图与显式领域编辑入口 @returns 版本固定的自有契约候选表单。 */
export function ServiceContractPanel({ node, graph, blocked, onEdit }: Readonly<{ node: ServiceNode; graph: GraphState; blocked: boolean; onEdit: (...operations: [GraphOperation, ...GraphOperation[]]) => void }>) {
  const active = graph.serviceContracts.find(value => value.serviceId === node.id)
  const bindings = graph.bindings.filter(value => value.source.componentId === node.id)
  const [text, setText] = useState(() => active ? JSON.stringify(active.document, null, 2) : '')
  const [reconfirm, setReconfirm] = useState(false)
  const [mapping, setMapping] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const parsed = readDocument(text)
  const submit = (event: React.SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (blocked) return
    if (!parsed.value) { setError(parsed.error); return }
    const reconnects = reconfirm ? reconfirmBindings(bindings, mapping) : []
    setError(null)
    onEdit({ type: 'service.setContract', serviceId: node.id, document: parsed.value }, ...reconnects)
  }
  return <section className="contract-panel" aria-label={`${node.displayName} 接口契约`}><h3>{node.displayName} 的接口契约</h3>
    {active ? <p className="field-help">已固定 {active.reference.id} / {active.reference.version}<code className="definition-digest">{active.reference.digest}</code></p> : <p>尚未定义接口和端口，保留为未就绪草稿。</p>}
    <p className="field-help">每个服务可定义自己的配置 schema、多份接口版本和具名端口。声明尚未验证源码实现，也不建立运行连接。</p>
    <button type="button" disabled={blocked} onClick={() => { setText(JSON.stringify(SERVICE_CONTRACT_EXAMPLE, null, 2)); setError(null) }}>载入端口示例</button>
    <form onSubmit={submit}><label>服务契约 JSON<textarea aria-label={`${node.displayName} 服务契约 JSON`} value={text} maxLength={32768} disabled={blocked} spellCheck={false} onChange={event => { setText(event.target.value) }} /></label>
      {bindings.length > 0 && <label className="confirm-bindings"><input type="checkbox" checked={reconfirm} disabled={blocked} onChange={event => { setReconfirm(event.target.checked) }} />同时重新确认现有数据库连接</label>}
      {reconfirm && <ContractMappings bindings={bindings} document={parsed.value} mapping={mapping} blocked={blocked} onChange={(id, port) => { setMapping(value => ({ ...value, [id]: port })) }} />}
      {error && <p role="alert" className="notice error">{error}</p>}<button type="submit" disabled={blocked}>保存服务契约</button>
    </form><p className="field-help">相同服务的契约 ID / 版本不可改义；内容变化请发布新版本，已有绑定须显式重新确认。</p>
  </section>
}

/** @param bindings 需明确重连的原关系 @param mapping 用户指定的新端口 @returns 与契约变更同批提交的显式重连操作。 */
function reconfirmBindings(bindings: DatabaseBinding[], mapping: Record<string, string>): GraphOperation[] {
  return bindings.map(binding => ({ type: 'binding.reconnect', bindingId: binding.id, source: { ...binding.source, portId: mapping[binding.id] ?? binding.source.portId }, target: binding.target, databaseName: binding.databaseName, purpose: binding.purpose }))
}

/** @param props 已有关系与当前 JSON 候选的端口 @returns 可审阅的新端口映射，不自动选择不存在的端口。 */
function ContractMappings({ bindings, document, mapping, blocked, onChange }: Readonly<{ bindings: DatabaseBinding[]; document: ServiceContractDocument | null; mapping: Record<string, string>; blocked: boolean; onChange: (id: string, port: string) => void }>) {
  const ports = document?.ports.filter(port => port.role === 'consume') ?? []
  return <div className="contract-mappings">{bindings.map(binding => {
    const selected = mapping[binding.id] ?? binding.source.portId
    return <label key={binding.id}>连接 {binding.databaseName} 的消费端口<select aria-label={`连接${binding.databaseName}的新端口`} value={selected} disabled={blocked} onChange={event => { onChange(binding.id, event.target.value) }}>{!ports.some(port => port.id === selected) && <option value={selected}>原端口已不可用：{selected}</option>}{ports.map(port => <option key={port.id} value={port.id}>{port.id} · {port.protocol} / {port.contract}</option>)}</select></label>
  })}</div>
}
