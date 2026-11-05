/** React Flow 只投影服务身份和领域草稿，连线和业务执行由后续切片交付。 */
import { Background, BackgroundVariant, Controls, ReactFlow, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { CanvasPosition, CanvasState, Position, Project } from '../contract/generated'

type ServiceCanvasNode = Node<{ label: string; path: string; commit: string | null }, 'service'>
type MiddlewareCanvasNode = Node<{ label: string; version: string; reasons: string[] }, 'mysql'>
type CanvasNode = ServiceCanvasNode | MiddlewareCanvasNode

/** @param props 具名服务投影 @returns 不含构建或运行状态猜测的节点。 */
function ServiceCard({ data }: NodeProps<ServiceCanvasNode>) {
  return <div className="service-card"><div className="service-card-top"><span className="service-symbol" aria-hidden="true">⌘</span><span className="draft-tag">待配置</span></div><strong>{data.label}</strong><span className="service-path" title={data.path}>{data.path}</span><div className="service-card-foot"><span className="git-dot" />{data.commit ? `登记快照 ${data.commit.slice(0, 7)}` : '暂无源提交'}<span>Git</span></div></div>
}

/** @param props 标准组件声明 @returns 只显示配置就绪程度的 MySQL 草稿节点。 */
function MySQLCard({ data }: NodeProps<MiddlewareCanvasNode>) {
  return <div className="service-card mysql-card"><div className="service-card-top"><span className="service-symbol" aria-hidden="true">▤</span><span className="draft-tag">配置草稿</span></div><strong>{data.label}</strong><span className="service-path">MySQL · 定义 {data.version}</span><div className="service-card-foot">{data.reasons.length ? '运行配置未完整' : '配置已完整 · 尚未运行'}</div><small>提供端口 mysql</small></div>
}

const nodeTypes = { service: ServiceCard, mysql: MySQLCard }

/** @param props 确认快照、交互草稿与位置命令 @returns 本机服务画布。 */
export function Canvas({ project, canvas, draft, blocked, onMove }: Readonly<{ project: Project; canvas: CanvasState; draft: CanvasPosition[]; blocked: boolean; onMove: (id: string, position: Position) => void }>) {
  const services: ServiceCanvasNode[] = project.nodes.map(node => ({ id: node.id, type: 'service', position: draft.find(item => item.componentId === node.id) ?? node.position, data: { label: node.displayName, path: node.repository.path, commit: node.repository.headCommit } }))
  const middlewares: MiddlewareCanvasNode[] = canvas.middlewares.map(node => ({ id: node.id, type: 'mysql', position: draft.find(item => item.componentId === node.id) ?? node.position, data: { label: node.displayName, version: node.definition.version, reasons: node.notReadyReasons } }))
  const nodes: CanvasNode[] = [...services, ...middlewares]
  return <div className="canvas" data-testid="canvas"><ReactFlow<CanvasNode> nodes={nodes} nodeTypes={nodeTypes} edges={[]} nodesDraggable={!blocked} nodesConnectable={false} elementsSelectable={false} minZoom={0.2} maxZoom={2} fitView={nodes.length > 0} fitViewOptions={{ maxZoom: 1, padding: 0.3 }} onNodesChange={changes => { for (const change of changes) if (change.type === 'position' && change.position && !blocked) onMove(change.id, change.position) }} proOptions={{ hideAttribution: true }} ariaLabelConfig={{ 'controls.zoomIn.ariaLabel': '放大画布', 'controls.zoomOut.ariaLabel': '缩小画布', 'controls.fitView.ariaLabel': '适应画布', 'node.a11yDescription.default': '拖动节点整理布局，也可以在右侧修改坐标' }}>
    <Background variant={BackgroundVariant.Dots} gap={24} size={1.2} color="#d8d8cc" /><Controls showInteractive={false} />
  </ReactFlow><div className="canvas-caption"><span>LOCAL CANVAS</span><span>{project.nodes.length} 个服务 · {middlewares.length} 个中间件 · 草稿空间</span></div>{nodes.length === 0 && <div className="canvas-empty"><div className="empty-diagram" aria-hidden="true">⌘<span>＋</span></div><h2>让第一个组件就位</h2><p>登记已有 Git 仓库，或从目录加入 MySQL，<br />它会成为这张画布上的一个节点。</p><span className="empty-step">01 — 加入组件 02 — 整理布局 03 — 保存</span></div>}</div>
}
