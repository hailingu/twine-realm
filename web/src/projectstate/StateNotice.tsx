/** 状态运输与远端草稿冲突分别呈现，不把通知到达显示成保存成功。 */
import { useState } from 'react'
import type { GraphSnapshot } from '../contract/generated'
import type { FeedPhase } from './feed'

/** @param props 流阶段和读取游标 @returns 独立于画布保存状态的简明连接状态。 */
export function StateNotice({ phase, cursor, capturedAt, message, reconnect }: Readonly<{ phase: FeedPhase; cursor: string | undefined; capturedAt: string | undefined; message: string; reconnect: () => void }>) {
  const labels = { connecting: '正在读取完整状态', catchingUp: '正在追平状态通知', live: '状态流已恢复', disconnected: '状态连接已断开 · 保留最后事实', failed: '状态读取需重新连接' }
  return <div className={`state-notice ${phase}`}><output aria-label="状态流连接" data-cursor={cursor}>{labels[phase]}</output>{capturedAt && <small>核实于 {new Date(capturedAt).toLocaleTimeString()}</small>}{message && <p>{message}</p>}{phase === 'failed' && <button type="button" onClick={reconnect}>重新连接状态</button>}</div>
}

/** @param props 远端图与本地候选拥有者 @returns 明确比较和保留候选的恢复入口。 */
export function RemoteStateNotice({ remote, blocked, apply }: Readonly<{ remote: GraphSnapshot; blocked: boolean; apply: () => void }>) {
  const [expanded, setExpanded] = useState(false)
  return <section className="notice remote-change" aria-label="远端画布变更"><strong>其他窗口已保存版本 {remote.project.revision}</strong><p>当前布局、连线与表单候选已保留，尚未改变你的编辑基准。</p><button type="button" onClick={() => { setExpanded(previous => !previous) }}>{expanded ? '收起远端声明' : '查看远端声明'}</button>
    {expanded && <div aria-label="远端已保存声明"><ul>{remote.project.nodes.map(node => <li key={node.id}>{node.displayName}</li>)}{remote.graph.middlewares.map(node => <li key={node.id}>{node.displayName}<pre>{JSON.stringify(node.configuration, null, 2)}</pre></li>)}</ul>{remote.graph.serviceContracts.map(value => <details key={value.serviceId}><summary>远端服务接口 {value.reference.id} / {value.reference.version}</summary><pre>{JSON.stringify(value.document, null, 2)}</pre></details>)}<ul>{remote.graph.bindings.map(binding => <li key={binding.id}>{binding.databaseName} · {binding.source.portId} → {binding.target.portId} · {binding.purpose}</li>)}</ul></div>}
    <button type="button" disabled={blocked} onClick={() => { if (window.confirm('载入远端版本并保留当前布局、连线与表单候选？请先比较远端声明，载入后重新核实候选再保存。')) apply() }}>载入远端版本并保留我的草稿</button>{blocked && <p>请先核实原保存请求，状态通知不会替你确认该请求。</p>}
  </section>
}
