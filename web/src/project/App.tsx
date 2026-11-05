/** 项目入口只展示服务端项目；切换工作区通过组件身份隔离异步会话。 */
import { useState } from 'react'
import type { CanvasSnapshot } from '../contract/generated'
import { PROJECT_API, type ProjectAPI } from './api'
import { ProjectPicker } from './ProjectPicker'
import { Workspace } from './Workspace'
import { RUNTIME_API, type RuntimeAPI } from '../runtime/api'

/** @param props 可注入真实边界的测试端口 @returns 本地项目工作台。 */
export function App({ api = PROJECT_API, runtimeApi = RUNTIME_API }: Readonly<{ api?: ProjectAPI; runtimeApi?: RuntimeAPI }>) {
  const [project, setProject] = useState<CanvasSnapshot | null>(null)
  return <div className="app-shell">
    <header className="app-header"><a className="brand" href="/" aria-label="Twine Realm 首页"><span className="brand-mark">t<span>r</span></span>twine realm<span className="brand-divider" />本地工作台</a><div className="local-badge"><span />仅在本机</div></header>
    {project ? <Workspace key={project.project.id} initial={project.project} initialCanvas={project.canvas} api={api} runtimeApi={runtimeApi} onClose={() => { setProject(null) }} /> : <ProjectPicker api={api} onOpen={setProject} />}
  </div>
}
