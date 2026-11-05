/** 启动本地项目工作台，业务状态由服务端快照和待提交草稿共同驱动。 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './project/App'
import './style.css'
const root = document.getElementById('root')
if (!root) throw new Error('页面缺少应用根节点')
createRoot(root).render(<StrictMode><App /></StrictMode>)
