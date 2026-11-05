/** 项目选择和创建拥有独立生命周期，创建结果不明时复用原请求身份。 */
import { useEffect, useRef, useState } from 'react'
import type { CreateCommand, CanvasSnapshot, Summary } from '../contract/generated'
import { RequestError, type ProjectAPI } from './api'

/** @param props 项目端口与打开回调 @returns 本地项目入口。 */
export function ProjectPicker({ api, onOpen }: Readonly<{ api: ProjectAPI; onOpen: (project: CanvasSnapshot) => void }>) {
  const [projects, setProjects] = useState<Summary[]>([])
  const [name, setName] = useState('')
  const [message, setMessage] = useState('正在读取本地项目…')
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<CreateCommand | null>(null)
  const session = useRef(new AbortController())
  const inFlight = useRef(false)
  useEffect(() => {
    const controller = new AbortController(); session.current = controller
    api.list(controller.signal).then(value => { if (!controller.signal.aborted) { setProjects(value.projects); setMessage('') } }).catch((error: unknown) => { if (!controller.signal.aborted) setMessage(error instanceof Error ? error.message : '无法读取本地项目') })
    return () => { controller.abort() }
  }, [api])
  useEffect(() => {
    if (!pending && !busy) return
    const handleUnload = (event: BeforeUnloadEvent) => { event.preventDefault() }
    window.addEventListener('beforeunload', handleUnload)
    return () => { window.removeEventListener('beforeunload', handleUnload) }
  }, [pending, busy])
  const execute = async (command: CreateCommand | null, id?: string, verify = false) => {
    if (inFlight.current) return
    inFlight.current = true; setBusy(true); setMessage('')
    const controller = session.current
    try {
      const project = await loadSelectedProject(api, command, id, verify, controller.signal)
      if (!controller.signal.aborted) { onOpen(project) }
    } catch (error) {
      if (controller.signal.aborted) return
      setMessage(error instanceof Error ? error.message : '本地请求失败')
      if (command) setPending(verify || !(error instanceof RequestError) || error.status === 0 || error.status >= 500 ? command : null)
    } finally { inFlight.current = false; if (!controller.signal.aborted) setBusy(false) }
  }
  return <main className="project-home">
    <div className="intro"><p className="eyebrow">YOUR LOCAL WORKSPACE</p><h1>从一个仓库开始，<br /><span>连接你的构想。</span></h1><p className="intro-copy">把已有服务放上画布，整理它们的位置。<br />项目与仓库关联保存于本机。</p><div className="intro-art" aria-hidden="true"><span className="art-dot" /><div className="art-node">服务仓库<span>Git · 草稿</span></div><div className="art-corner" /></div><p className="scope-note">支持仓库登记、源码固定、MySQL 配置草稿与布局保存。组件尚未运行。</p></div>
    <section className="project-panel" aria-labelledby="projects-title"><div className="section-heading"><div><p className="eyebrow">PROJECTS</p><h2 id="projects-title">本地项目</h2></div><span className="count">{projects.length.toString().padStart(2, '0')}</span></div>
      <form onSubmit={event => { event.preventDefault(); if (!busy && !pending && name.trim()) void execute({ mutationId: crypto.randomUUID(), name: name.trim() }) }}><label htmlFor="project-name">新项目名称</label><div className="input-row"><input id="project-name" value={name} maxLength={80} placeholder="例如：我的服务空间" disabled={busy || !!pending} onChange={event => { setName(event.target.value) }} /><button type="submit" className="primary" disabled={busy || !!pending || !name.trim()}>{busy ? '处理中…' : '创建项目'}<span aria-hidden="true">↗</span></button></div></form>
      {message && <output className="notice">{message}</output>}
      {pending && <div className="recovery"><strong>创建结果尚未确认</strong><p>保留原请求，先核实或使用相同身份重试。</p><button type="button" disabled={busy} onClick={() => { void execute(pending, undefined, true) }}>核实创建结果</button><button type="button" disabled={busy} onClick={() => { void execute(pending) }}>重试原请求</button></div>}
      <div className="project-list">{projects.map(item => <button type="button" className="project-row" key={item.id} disabled={busy || !!pending} onClick={() => { void execute(null, item.id) }}><span className="project-icon" aria-hidden="true">⌘</span><span><strong>{item.name}</strong><small>{item.nodeCount} 个服务 · 版本 {item.revision}</small></span><span className="row-arrow" aria-hidden="true">↗</span></button>)}</div>
      {!projects.length && !busy && !message && <div className="empty-project"><span aria-hidden="true">＋</span><h3>第一张画布，等你开始</h3><p>创建项目后，即可登记已有的本地 Git 仓库。</p></div>}
      <footer className="panel-foot"><span aria-hidden="true">◈</span> 本机保存 · 不修改仓库内容</footer>
    </section>
  </main>
}

/** @param api 外部端口 @param command 可恢复创建意图 @param id 已存在项目 @param verify 只核实原结果 @param signal 会话取消 @returns 已确认项目。 */
async function loadSelectedProject(api: ProjectAPI, command: CreateCommand | null, id: string | undefined, verify: boolean, signal: AbortSignal): Promise<CanvasSnapshot> {
  if (!command) {
    if (!id) throw new Error("缺少项目身份")
    return api.canvas(id, signal)
  }
  const receipt = verify ? await api.receipt(command.mutationId, signal) : await api.create(command, signal)
  if (receipt.mutationId !== command.mutationId || receipt.kind !== "createProject") {
    throw new RequestError({ code: "INVALID_RESPONSE", message: "创建回执不匹配，请核实原请求" }, 0)
  }
  return api.canvas(receipt.project.id, signal)
}
