/** React Flow 只投影确认图；连接手势产生待确认候选，不直接改变持久关系。 */
import { useEffect } from 'react'
import { Background, BackgroundVariant, Controls, Handle, MarkerType, Position as HandlePosition, ReactFlow, useUpdateNodeInternals, type Connection, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { CanvasPosition, GraphState, Position, Project, ServicePort } from '../contract/generated'
import { MYSQL_PORTS } from '../contract/graph.generated'
import { missingPorts, servicePorts } from './graphModel'

type ServiceCanvasNode = Node<{ label: string; path: string; commit: string | null; ports: ServicePort[]; missing: string[]; declared: boolean }, 'service'>
type MiddlewareCanvasNode = Node<{ label: string; version: string; reasons: string[] }, 'mysql'>
type CanvasNode = ServiceCanvasNode | MiddlewareCanvasNode

/** @param props 具名服务投影 @returns 仅来自明确服务契约的端口和就绪提示。 */
function ServiceCard({ id, data, isConnectable }: NodeProps<ServiceCanvasNode>) {
  const updateInternals = useUpdateNodeInternals()
  const portKeys = data.ports.map(port => `${port.id}/${port.role}`).join('|')
  useEffect(() => { updateInternals(id) }, [id, portKeys, updateInternals])
  let status = '声明草稿'
  if (!data.declared) status = '待配置 · 未定义契约'
  else if (data.missing.length) status = '待配置 · 缺少依赖绑定'
  return <div className="service-card"><div className="service-card-top"><span className="service-symbol" aria-hidden="true">⌘</span><span className="draft-tag">{status}</span></div><strong>{data.label}</strong><span className="service-path" title={data.path}>{data.path}</span><div className="service-card-foot"><span className="git-dot" />{data.commit ? `登记快照 ${data.commit.slice(0, 7)}` : '暂无源提交'}<span>Git</span></div>
    <div className="semantic-ports">{data.ports.map((port, index) => <div className={`semantic-port ${port.role}`} key={port.id}>
      <Handle id={port.id} type={port.role === 'consume' ? 'source' : 'target'} position={port.role === 'consume' ? HandlePosition.Right : HandlePosition.Left} isConnectable={isConnectable} style={{ top: 175 + index * 28 }} aria-label={`${data.label} ${port.role === 'consume' ? '消费' : '提供'}端口 ${port.id}`} />
      <span>{port.role === 'consume' ? '消费 →' : '← 提供'} {port.id}</span><small>{port.protocol} / {port.contract}</small>
    </div>)}</div>
    {data.missing.length > 0 && <small className="missing-ports">待绑定：{data.missing.join('、')}</small>}
  </div>
}

/** @param props 标准组件声明 @returns 内置目录定义的具名 MySQL 提供端口。 */
function MySQLCard({ data, isConnectable }: NodeProps<MiddlewareCanvasNode>) {
  return <div className="service-card mysql-card"><div className="service-card-top"><span className="service-symbol" aria-hidden="true">▤</span><span className="draft-tag">配置草稿</span></div><strong>{data.label}</strong><span className="service-path">MySQL · 定义 {data.version}</span><div className="service-card-foot">{data.reasons.length ? '运行配置未完整' : '配置已完整 · 尚未运行'}</div>
    <div className="semantic-ports">{MYSQL_PORTS.map(port => <div className="semantic-port provide" key={port.id}><Handle id={port.id} type="target" position={HandlePosition.Left} isConnectable={isConnectable} style={{ top: 175 }} aria-label={`${data.label} 提供端口 ${port.id}`} /><span>← 提供 {port.id}</span><small>{port.protocol} / {port.contract}</small></div>)}</div>
  </div>
}

const nodeTypes = { service: ServiceCard, mysql: MySQLCard }

/** @param project 服务身份 @param graph 完整确认图 @param draft 本地位置 @returns 不赋予 React Flow 领域状态所有权的节点投影。 */
function projectNodes(project: Project, graph: GraphState, draft: CanvasPosition[]): CanvasNode[] {
  const services: ServiceCanvasNode[] = project.nodes.map(node => ({ id: node.id, type: 'service', position: draft.find(item => item.componentId === node.id) ?? node.position, data: { label: node.displayName, path: node.repository.path, commit: node.repository.headCommit, ports: servicePorts(graph, node.id), missing: missingPorts(graph, node.id), declared: graph.serviceContracts.some(value => value.serviceId === node.id) } }))
  const middlewares: MiddlewareCanvasNode[] = graph.middlewares.map(node => ({ id: node.id, type: 'mysql', position: draft.find(item => item.componentId === node.id) ?? node.position, data: { label: node.displayName, version: node.definition.version, reasons: node.notReadyReasons } }))
  return [...services, ...middlewares]
}

/** @param props 确认图、交互草稿与候选命令 @returns 支持具名端口和依赖重连的本机画布。 */
export function Canvas({ project, canvas, draft, blocked, connectionBlocked, onMove, onConnect, onReconnect }: Readonly<{ project: Project; canvas: GraphState; draft: CanvasPosition[]; blocked: boolean; connectionBlocked: boolean; onMove: (id: string, position: Position) => void; onConnect: (connection: Connection) => void; onReconnect: (id: string, connection: Connection) => void }>) {
  const nodes = projectNodes(project, canvas, draft)
  const edges: Edge[] = canvas.bindings.map(binding => ({ id: binding.id, source: binding.source.componentId, sourceHandle: binding.source.portId, target: binding.target.componentId, targetHandle: binding.target.portId, label: `${binding.source.portId} → ${binding.target.portId} · ${binding.databaseName} · 声明未应用`, markerEnd: { type: MarkerType.ArrowClosed }, ariaLabel: `数据库依赖 ${binding.databaseName}`, reconnectable: !connectionBlocked }))
  return <div className="canvas" data-testid="canvas"><ReactFlow<CanvasNode> nodes={nodes} nodeTypes={nodeTypes} edges={edges} nodesDraggable={!blocked} nodesConnectable={!connectionBlocked} edgesReconnectable={!connectionBlocked} deleteKeyCode={null} minZoom={0.2} maxZoom={2} fitView={nodes.length > 0} fitViewOptions={{ maxZoom: 1, padding: 0.3 }} onConnect={onConnect} onReconnect={(edge, connection) => { onReconnect(edge.id, connection) }} onNodesChange={changes => { for (const change of changes) if (change.type === 'position' && change.position && !blocked) onMove(change.id, change.position) }} proOptions={{ hideAttribution: true }} ariaLabelConfig={{ 'controls.zoomIn.ariaLabel': '放大画布', 'controls.zoomOut.ariaLabel': '缩小画布', 'controls.fitView.ariaLabel': '适应画布', 'node.a11yDescription.default': '拖动节点整理布局，或使用检查面板编辑具名接口与依赖' }}>
    <Background variant={BackgroundVariant.Dots} gap={24} size={1.2} color="#d8d8cc" /><Controls showInteractive={false} />
  </ReactFlow><div className="canvas-caption"><span>LOCAL CANVAS</span><span>{project.nodes.length} 个服务 · {canvas.middlewares.length} 个中间件 · {edges.length} 个依赖声明</span></div>{nodes.length === 0 && <div className="canvas-empty"><div className="empty-diagram" aria-hidden="true">⌘<span>＋</span></div><h2>让第一个组件就位</h2><p>登记已有 Git 仓库，或从目录加入 MySQL，<br />它会成为这张画布上的一个节点。</p><span className="empty-step">01 — 加入组件 02 — 整理布局 03 — 保存</span></div>}</div>
}
