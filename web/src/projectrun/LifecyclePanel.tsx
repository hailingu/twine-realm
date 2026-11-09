/** 项目父操作面板不根据通知确认请求，也不在卸载时取消后台操作。 */
import { submissionFailure } from './requestFailure'
import { clearRequest, readRequest, saveRequest } from './requestMemory'
import { useEffect, useRef, useState } from 'react'
import type { ProjectRunCommand } from '../contract/generated'
import { RequestError } from '../project/api'
import { useStateProjection } from '../projectstate/Projection'
import { checkReceipt, LIFECYCLE_API, type LifecycleAPI } from './api'

const STATES: Record<string, string> = { accepted: '已受理', executing: '执行中', waiting_retry: '等待重试', needs_attention: '部分完成或结果未知，需要核实', succeeded: '本次操作已核实', failed: '失败', superseded: '已替代', pending: '未开始', unknown: '结果未知', skipped: '跳过' }

/** @param props 当前项目及独立命令端口 @returns 原计划停止恢复与原请求核实入口。 */
export function LifecyclePanel({ id, revision, blocked, api = LIFECYCLE_API }: Readonly<{ id: string; revision: string; blocked: boolean; api?: LifecycleAPI }>) {
  const projection = useStateProjection()
  const life = projection?.snapshot?.lifecycle
  const [saved] = useState(() => readRequest(id))
  const pending = useRef<ProjectRunCommand | null>(saved.command)
  const sending = useRef(false)
  const epoch = useRef({ active: false })
  useEffect(() => { const session = { active: true }; epoch.current = session; return () => { session.active = false } }, [id])
  const [phase, setPhase] = useState<'idle' | 'sending' | 'unknown' | 'accepted' | 'rejected'>(saved.command ? 'unknown' : 'idle')
  const [message, setMessage] = useState(saved.error || (saved.command ? '已恢复原请求，请先核实受理结果。' : ''))
  const [requestId, setRequestId] = useState(saved.command?.mutationId ?? '')
  const perform = async (command: ProjectRunCommand, verify: boolean) => {
    if (sending.current) return
    const session = epoch.current
    sending.current = true; setPhase('sending')
    try {
      const receipt = verify ? await api.receipt(id, command.mutationId) : await api.accept(id, command)
      if (!session.active) return
      checkReceipt(receipt, id, command)
      clearRequest(id, command.mutationId); pending.current = null; setPhase('accepted'); setMessage('原请求已受理；实际停止或运行结果以后台核实事实为准。')
    } catch (error) {
      if (!session.active) return
      const failure = submissionFailure(error, verify, id, command.mutationId)
      if (failure.cleared) pending.current = null
      setPhase(failure.phase); setMessage(failure.message)
    } finally { if (session.active) sending.current = false }
  }
  const run = (desired: 'running' | 'stopped') => {
    if (!life || pending.current || sending.current) return
    if (!window.confirm(desired === 'stopped' ? '停止原应用后停止原MySQL，保留源码、候选、容器、卷与数据？' : '仅恢复原MySQL及原应用，不创建替代资源或重跑迁移？')) return
    const command: ProjectRunCommand = { mutationId: crypto.randomUUID(), baseRevision: revision, baseGeneration: life.generation, desired }
    try { saveRequest(id, command) }
    catch { setMessage('无法保存原请求，未发送命令。'); return }
    pending.current = command; setRequestId(command.mutationId); void perform(command, false)
  }
  const retryFailed = async () => {
    if (!life?.operation || sending.current) return
    const session = epoch.current
    sending.current = true; setPhase('sending')
    try {
      const receipt = await api.retry(id, life.operation.mutationId)
      if (!session.active) return
      if (receipt.operationId !== life.operation.id || receipt.mutationId !== life.operation.mutationId || receipt.target.projectId !== id || receipt.target.generation !== life.generation) throw new Error('恢复回执归属不一致')
      setMessage('已请求接续原操作，请读取后台事实。'); setPhase('accepted')
    } catch (error) {
      if (!session.active) return
      setMessage(error instanceof RequestError ? error.message : '接续结果未知，请重新读取原操作后核实。')
      setPhase('rejected')
    }
    finally { if (session.active) sending.current = false }
  }
  if (!projection?.managed) return null
  const active = life?.operation && !['succeeded', 'failed', 'needs_attention', 'superseded'].includes(life.operation.state)
  const disabled = Boolean(saved.error) || blocked || !life || Boolean(projection.error) || phase === 'sending' || phase === 'unknown' || Boolean(active)
  return <section aria-label="项目停止与恢复" className="runtime-panel"><h3>项目停止与恢复</h3>
    <p>保留原源码、固定候选、容器、数据卷和数据。停止先应用后数据库；恢复先数据库认证后原应用。</p>
    <button type="button" disabled={disabled || life?.target?.desired === 'stopped'} onClick={() => { run('stopped') }}>停止项目并保留数据</button>
    <button type="button" disabled={disabled || life?.target?.desired !== 'stopped' || life.operation?.state !== 'succeeded'} onClick={() => { run('running') }}>恢复原项目</button>
    {life?.operation?.state === 'failed' && !life.operation.steps.some(step => ['executing', 'unknown'].includes(step.state)) && <button type="button" disabled={disabled} onClick={() => { void retryFailed() }}>接续原失败操作</button>}
    {message && <output>{message}</output>}
    {phase === 'unknown' && <div role="alert"><p>请求结果未知，其他状态更新不能确认本次请求。原请求：{requestId}</p><button type="button" onClick={() => { if (pending.current) void perform(pending.current, true) }}>核实原项目请求</button><button type="button" onClick={() => { if (pending.current) void perform(pending.current, false) }}>重试原项目请求</button></div>}
    {life?.operation && <><p>父操作：{STATES[life.operation.state] ?? life.operation.state} · 代次 {life.generation}</p><p>{life.operation.message} {life.operation.code}</p><ol>{life.operation.steps.map(step => <li key={step.name}>{step.name}：{STATES[step.state] ?? step.state} {step.code}</li>)}</ol></>}
    {life?.facts && <p>应用停止核实：{life.facts.applicationStopped ? '已停止' : '未确认停止'}（{life.facts.applicationObservedAt}）；数据库停止核实：{life.facts.databaseStopped ? '已停止' : '未确认停止'}（{life.facts.databaseObservedAt}）。历史认证与迁移证明不等于当前在线。</p>}
    <p>关闭页面或状态连接不会取消已受理命令。未知外部动作不能直接反向恢复。</p>
  </section>
}
