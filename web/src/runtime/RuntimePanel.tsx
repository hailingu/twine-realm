/** MySQL详情区分运行目标、持久操作与实际事实；数据删除不属于此入口。 */
import { useEffect, useRef, useState } from 'react'
import type { MiddlewareNode, RuntimeEventPage, RuntimeSnapshot } from '../contract/generated'
import { RequestError } from '../project/api'
import type { RuntimeAPI } from './api'
import { runtimeLabel, submissionPending, type RuntimeState } from './model'
import type { useRuntime } from './useRuntime'
import './runtime.css'

type Controller = ReturnType<typeof useRuntime>
const OPERATION_NAMES = { accepted: '已受理', executing: '执行中', waiting_retry: '等待重试', needs_attention: '需要人工核实', succeeded: '本次操作已核实', failed: '失败', superseded: '已被新目标替代' }
const STEP_NAMES = { preflight: '运行预检', credentials: '秘密与配置', network: '项目私网', volume: '持久数据卷', container: 'MySQL容器', readiness: '认证就绪', stop: '停止容器', stopped: '停止核实' }
const STEP_STATES = { pending: '未开始', executing: '执行中', unknown: '结果未知', succeeded: '已核实', failed: '失败', skipped: '按计划跳过' }

/** @param props 已保存声明、同一运行所有者与布局保护 @returns 可明确接受及恢复的本机运行面板。 */
export function RuntimePanel({ projectId, revision, node, runtime, api, blocked }: Readonly<{ projectId: string; revision: string; node: MiddlewareNode; runtime: Controller; api: RuntimeAPI | undefined; blocked: boolean }>) {
  const { state } = runtime
  const value = state.snapshot
  const target = value?.target
  const foreign = target !== null && target !== undefined && target.resource.componentId !== node.id
  const configured = state.capability?.enabled === true
  const pending = submissionPending(state)
  const active = value?.operation && !['needs_attention', 'succeeded', 'failed', 'superseded'].includes(value.operation.state)
  const changed = target && !foreign && JSON.stringify(target.resource.configuration) !== JSON.stringify(node.configuration)
  const disabled = blocked || pending || !configured || !value || state.readError !== null || foreign
  const memory = typeof node.configuration.memoryMiB === 'number' ? node.configuration.memoryMiB : null
  const status = runtimeLabel(state, node.id, state.now)
  const run = () => { if (window.confirm(`使用已保存配置接受MySQL运行目标？内存 ${memory ?? '未设置'} MiB。`)) runtime.run(node.id, revision, 'running') }
  const stop = () => { if (window.confirm('停止此项目MySQL并保留数据卷和运行秘密？')) runtime.run(node.id, revision, 'stopped') }
  return <section className="runtime-panel" aria-label="MySQL运行状态"><h3>MySQL运行</h3>
    <output className={`runtime-fact ${status.tone}`} aria-label="MySQL运行事实">{status.text}</output>
    {state.readError && <p role="alert" className="notice error">{state.readError}。最后事实保留，当前状态不能确认。</p>}
    {!configured && <p className="field-help">{state.capability?.message ?? '正在读取本机运行能力…'}。按仓库运行说明显式初始化私有运行目录后启用。</p>}
    {foreign && <p className="notice" role="alert">首版每项目管理一个固定MySQL运行实例，当前已关联其他组件。</p>}
    <SubmissionNotice state={state} runtime={runtime} />
    {!foreign && <>
      <p className="field-help">按已保存配置运行：MySQL 9.7.4 · 内存 {memory ?? '未设置'} MiB。数据库不发布宿主端口。</p>
      {changed && <p className="notice" role="alert">声明与固定运行配置不同。停止仍使用原实例；重新运行前请还原声明，配置升级将在后续开放。</p>}
      <div className="runtime-actions"><button type="button" disabled={disabled || memory === null || node.notReadyReasons.length > 0 || Boolean(changed) || Boolean(active && target?.desired === 'running')} onClick={run}>{target?.desired === 'running' ? '重新核实运行目标' : '确认运行 MySQL'}</button><button type="button" disabled={disabled || !target || Boolean(active && target.desired === 'stopped')} onClick={stop}>确认停止 MySQL</button></div>
      {value && <OperationDetails value={value} />}
      {api && value?.operation && <RuntimeEvents key={value.operation.id} api={api} projectId={projectId} />}
    </>}
    <p className="field-help">运行目标由后台持续执行。关闭页面不取消已受理操作；关闭控制面与停止MySQL分别处理。</p>
  </section>
}

/** @param props 原请求状态和恢复入口 @returns 不以受理或重试冒充外部成功的反馈。 */
function SubmissionNotice({ state, runtime }: Readonly<{ state: RuntimeState; runtime: Controller }>) {
  const value = state.submission
  if (value.kind === 'idle') return null
  if (value.kind === 'sending') return <output>正在核实运行目标的受理结果…</output>
  if (value.kind === 'accepted') return <output>目标已受理，正在读取后台操作进度。</output>
  if (value.kind === 'rejected') return <p role="alert" className="notice error">目标未受理：{value.message}</p>
  return <div role="alert" className="recovery"><strong>运行请求结果尚未确认</strong><p>{value.message}</p><button type="button" disabled={value.verifying} onClick={() => { runtime.verify() }}>核实原运行请求</button><button type="button" disabled={value.verifying} onClick={() => { runtime.retry() }}>重试原运行请求</button><small>原请求 {value.command.mutationId}</small></div>
}

/** @param props 独立运行快照 @returns 目标、操作、步骤和采集时间的可追溯诊断。 */
function OperationDetails({ value }: Readonly<{ value: RuntimeSnapshot }>) {
  const operation = value.operation
  let desired = '尚未接受'
  if (value.target) desired = value.target.desired === 'running' ? '运行' : '停止'
  const freshness = { fresh: '近期采集', stale: '已过期', unknown: '当前未知' }[value.freshness]
  return <div className="runtime-details">
    <p>运行目标：{desired} · 目标代次 {value.generation}</p>
    {operation && <><p data-testid="runtime-operation-state">操作：{OPERATION_NAMES[operation.state]} · 尝试 {operation.attempts}</p>{operation.message && <p className={operation.state === 'needs_attention' ? 'notice error' : 'field-help'}>{operation.message} {operation.code && <code>{operation.code}</code>}</p>}<ol className="runtime-steps">{operation.steps.map(step => <li key={step.name}><span>{STEP_NAMES[step.name]}</span><span>{STEP_STATES[step.state]}</span>{step.code && <code>{step.code}</code>}</li>)}</ol></>}
    <p className="field-help">最后事实：{value.observation?.observedAt ?? '尚未取得'} · {freshness} {value.observationCode && <code>{value.observationCode}</code>}</p>
    {value.target && <details><summary>资源与操作详情</summary><dl><dt>固定镜像</dt><dd><code>{value.target.resource.image}</code></dd><dt>网络</dt><dd><code>{value.target.resource.network}</code></dd><dt>数据卷</dt><dd><code>{value.target.resource.volume}</code></dd><dt>操作</dt><dd><code>{operation?.id}</code></dd><dt>操作版本 / 事件序号</dt><dd>{operation?.version} / {value.sequence}</dd><dt>数据实例身份</dt><dd><code>{value.observation?.serverUuid || '当前不可读取'}</code></dd></dl></details>}
  </div>
}

/** @param props 只读事件端口和项目 @returns 有界、显式翻页的持久事件记录。 */
function RuntimeEvents({ api, projectId }: Readonly<{ api: RuntimeAPI; projectId: string }>) {
  const [page, setPage] = useState<RuntimeEventPage | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const lifetime = useRef<AbortController | null>(null)
  const inFlight = useRef(false)
  useEffect(() => { const controller = new AbortController(); lifetime.current = controller; return () => { controller.abort() } }, [api, projectId])
  const load = async (sequence: string) => {
    const signal = lifetime.current?.signal
    if (!signal || signal.aborted || inFlight.current) return
    inFlight.current = true; setBusy(true)
    try { const value = await api.events(projectId, sequence, signal); if (!signal.aborted) { setPage(value); setError(null) } }
    catch (error) { if (!signal.aborted) setError(error instanceof RequestError ? error.failure.message : '运行事件尚未读取，请重试') }
    finally { if (!signal.aborted) { inFlight.current = false; setBusy(false) } }
  }
  return <details className="runtime-events"><summary>持久运行事件</summary><button type="button" disabled={busy} onClick={() => { void load('0') }}>读取运行事件</button>{error && <p role="alert">{error}</p>}{page && <><ol>{page.events.map(event => <li key={event.sequence}><code>{event.sequence}</code> {event.kind} · {event.code} <small>{event.createdAt}</small></li>)}</ol>{page.hasMore && <button type="button" disabled={busy} onClick={() => { void load(page.nextSequence) }}>读取下一页事件</button>}</>}</details>
}
