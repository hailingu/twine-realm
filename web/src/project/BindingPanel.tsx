/** 数据库依赖通过显式端点表单提交，画布手势只填候选，不改变确认图。 */
import { useState } from 'react'
import type { DatabaseBinding, GraphOperation, GraphState, PortEndpoint, Project } from '../contract/generated'
import { MYSQL_PORTS } from '../contract/graph.generated'
import { parseContract } from './api'
import { servicePorts, type BindingCandidate } from './graphModel'

/** @param props 已确认项目、图、候选和单一编辑入口 @returns 可用键盘创建或重连的表单。 */
export function BindingPanel({ project, graph, candidate, blocked, onEdit }: Readonly<{ project: Project; graph: GraphState; candidate: BindingCandidate; blocked: boolean; onEdit: (...operations: [GraphOperation, ...GraphOperation[]]) => void }>) {
  const [source, setSource] = useState<PortEndpoint>(candidate.source ?? { componentId: '', portId: '' })
  const [target, setTarget] = useState<PortEndpoint>(candidate.target ?? { componentId: '', portId: '' })
  const [name, setName] = useState(candidate.databaseName)
  const [purpose, setPurpose] = useState<DatabaseBinding['purpose']>(candidate.purpose)
  const [error, setError] = useState<string | null>(null)
  const ports = servicePorts(graph, source.componentId).filter(port => port.role === 'consume')
  const submit = (event: React.SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (blocked) return
    try {
      const value = { type: candidate.bindingId ? 'binding.reconnect' : 'binding.create', ...(candidate.bindingId ? { bindingId: candidate.bindingId } : {}), source, target, databaseName: name.trim(), purpose }
      const operation = parseContract<GraphOperation>('GraphOperation', value)
      setError(null); onEdit(operation)
    } catch { setError('请明确选择两端语义端口，数据库名称使用 1–48 位小写字母、数字和下划线，并以字母开头。') }
  }
  return <section className="binding-panel" aria-label="数据库依赖候选"><h3>{candidate.bindingId ? '重连数据库依赖' : '建立数据库依赖'}</h3><p className="field-help">消费服务 → 提供 MySQL。确认只保存声明，当前不会建库、创建账号或使连接生效。</p>
    <form onSubmit={submit} noValidate><label>消费服务<select aria-label="依赖消费服务" value={source.componentId} disabled={blocked} onChange={event => { const id = event.target.value; setSource({ componentId: id, portId: '' }); if (!name) setName(`app_${id.replaceAll('-', '').slice(0, 12)}`) }}><option value="">请选择已有服务</option>{source.componentId && !project.nodes.some(node => node.id === source.componentId) && <option value={source.componentId}>原选择已不可用</option>}{project.nodes.map(node => <option key={node.id} value={node.id}>{node.displayName}</option>)}</select></label>
      <label>消费端口<select aria-label="依赖消费端口" value={source.portId} disabled={blocked} onChange={event => { setSource(value => ({ ...value, portId: event.target.value })) }}><option value="">请选择已声明端口</option>{source.portId && !ports.some(port => port.id === source.portId) && <option value={source.portId}>原端口已不可用：{source.portId}</option>}{ports.map(port => <option key={port.id} value={port.id}>{port.id} · {port.protocol} / {port.contract}</option>)}</select></label>
      {!ports.length && <p className="field-help">该服务尚无消费端口，先明确声明接口契约。</p>}
      <label>提供 MySQL<select aria-label="依赖提供 MySQL" value={target.componentId} disabled={blocked} onChange={event => { setTarget({ componentId: event.target.value, portId: '' }) }}><option value="">请选择标准 MySQL</option>{target.componentId && !graph.middlewares.some(node => node.id === target.componentId) && <option value={target.componentId}>原目标已不可用</option>}{graph.middlewares.map(node => <option key={node.id} value={node.id}>{node.displayName}</option>)}</select></label>
      <label>提供端口<select aria-label="依赖提供端口" value={target.portId} disabled={blocked} onChange={event => { setTarget(value => ({ ...value, portId: event.target.value })) }}><option value="">请选择提供端口</option>{target.portId && !MYSQL_PORTS.some(port => port.id === target.portId) && <option value={target.portId}>原端口已不可用：{target.portId}</option>}{MYSQL_PORTS.map(port => <option key={port.id} value={port.id}>{port.id} · {port.protocol} / {port.contract}</option>)}</select></label>
      <label>数据库名称<input aria-label="依赖数据库名称" value={name} maxLength={48} disabled={blocked} spellCheck={false} onChange={event => { setName(event.target.value) }} /></label>
      <label>数据库用途<select aria-label="依赖数据库用途" value={purpose} disabled={blocked} onChange={event => { const value = event.target.value; if (value === 'readOnly' || value === 'readWrite') setPurpose(value) }}><option value="readWrite">应用读写</option><option value="readOnly">应用只读</option></select></label>
      {error && <p className="notice error" role="alert">{error}</p>}<button type="submit" disabled={blocked}>{candidate.bindingId ? '确认重连依赖' : '确认保存依赖'}</button>
    </form>
  </section>
}
