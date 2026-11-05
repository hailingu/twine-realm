/** 将登记快照、已固定源码与带时间的当前观察分别展示，不假冒构建就绪。 */
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ServiceNode, SourceObservation, SourceVersion } from '../contract/generated'
import { RequestError, type ProjectAPI } from './api'

/** @param api HTTP 端口 @param projectId 当前项目 @param node 当前服务 @param revision 已确认项目版本 @returns 可取消的只读观察和显式刷新入口。 */
function useObservation(api: ProjectAPI, projectId: string, node: ServiceNode, revision: string) {
  const controller = useRef<AbortController | null>(null)
  const [state, setState] = useState<{ value: SourceObservation | null; loading: boolean; error: string | null }>({ value: null, loading: false, error: null })
  const refresh = useCallback(async () => {
    controller.current?.abort()
    const current = new AbortController()
    controller.current = current
    setState(value => ({ ...value, loading: true, error: null }))
    try {
      const value = await api.source(projectId, node.id, current.signal)
      if (value.version.projectId !== projectId || value.version.serviceId !== node.id || value.version.repositoryId !== node.repository.id) throw new RequestError({ code: 'INVALID_RESPONSE', message: '仓库关联已变化，请载入最新项目后重试' }, 0)
      if (!current.signal.aborted) setState({ value, loading: false, error: null })
    } catch (error) {
      if (!current.signal.aborted) setState(value => ({ ...value, loading: false, error: error instanceof Error ? error.message : '读取源码状态失败' }))
    }
  }, [api, projectId, node.id, node.repository.id])
  useEffect(() => { void refresh(); return () => { controller.current?.abort() } }, [refresh, revision])
  return { ...state, refresh }
}

/** @param cached 已确认保存值 @param observed 最近读取值 @returns 最新项目版本中的固定源码，不将 HEAD 当固定值。 */
function latestVersion(cached: SourceVersion | undefined, observed: SourceVersion | undefined): SourceVersion | undefined {
  if (!cached) return observed
  return observed && BigInt(observed.projectRevision) > BigInt(cached.projectRevision) ? observed : cached
}

/** @param props 服务、已确认源码、只读端口和显式固定命令 @returns 独立源码详情与候选确认表单。 */
export function SourceVersionPanel({ projectId, revision, node, api, saved, blocked, dirty, onPin }: Readonly<{ projectId: string; revision: string; node: ServiceNode; api: ProjectAPI; saved: SourceVersion | undefined; blocked: boolean; dirty: boolean; onPin: (commit: string) => void }>) {
  const observation = useObservation(api, projectId, node, revision)
  const [candidate, setCandidate] = useState('')
  const version = latestVersion(saved, observation.value?.version)
  const git = observation.value?.git
  const disabled = blocked || dirty
  const valid = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(candidate.trim())
  return <section className="source-panel" aria-label={`${node.displayName} 源码版本`}>
    <div className="section-heading"><h3>{node.displayName} 的源码版本</h3><button type="button" disabled={blocked || observation.loading} onClick={() => { void observation.refresh() }}>刷新仓库状态</button></div>
    <dl><dt>登记时快照</dt><dd><code>{node.repository.headCommit ?? '登记时尚无提交'}</code></dd><dt>已固定 commit</dt><dd data-testid="pinned-commit"><code>{version ? version.pinnedCommit ?? '尚未固定' : '尚未读取固定版本'}</code></dd></dl>
    <p className="field-help">固定源码仍为草稿，不表示已构建或部署。</p>
    {observation.loading && <p>正在读取当前仓库…</p>}
    {observation.error && <p className="notice error" role="alert">刷新失败：{observation.error}。保留上次观察与已有选择。</p>}
    {git && <GitDetails value={git} stale={observation.error !== null || observation.loading} />}
    <form onSubmit={event => { event.preventDefault(); if (!disabled && valid) onPin(candidate.trim()) }}>
      <label htmlFor={`source-${node.id}`}>待固定完整 commit</label><input id={`source-${node.id}`} value={candidate} maxLength={64} spellCheck={false} autoComplete="off" disabled={disabled} onChange={event => { setCandidate(event.target.value) }} />
      <div className="source-actions"><button type="button" disabled={disabled || !git?.available || !git.headCommit || observation.loading || observation.error !== null} onClick={() => { if (git?.headCommit) setCandidate(git.headCommit) }}>选择当前 HEAD</button><button type="submit" disabled={disabled || !valid || candidate.trim() === version?.pinnedCommit}>确认固定 commit</button></div>
      <p className="field-help">只保存明确选定的已提交内容。刷新 HEAD 不会替换待确认选择，也不会包含未提交修改。</p>
      {dirty && <p className="field-help">先保存或还原布局，再固定源码。</p>}
    </form>
  </section>
}

/** @param props 带时间的 Git 事实及是否为上次观察 @returns 可用、未知和失联的明确展示。 */
function GitDetails({ value, stale }: Readonly<{ value: SourceObservation['git']; stale: boolean }>) {
  return <div className="git-observation"><small>{stale ? '上次观察' : '最近观察'}：<time dateTime={value.observedAt}>{new Date(value.observedAt).toLocaleString()}</time></small>
    {value.available ? <><p>{value.branch ? `分支 ${value.branch}` : '游离 HEAD'}{value.headCommit ? '' : ' · 尚无提交'}</p><p>当前 HEAD <code>{value.headCommit ?? '无提交'}</code></p><p>{worktreeLabel(value.dirty)}</p></> : <p className="notice error" role="alert">当前仓库不可用：{value.problem?.message ?? '请稍后刷新'}，已有固定记录保留。</p>}
    {value.pinnedAvailable === false && <p role="alert">已固定对象当前不可用，固定记录未删除。</p>}
    {value.warning && <p className="field-help">{value.warning}</p>}
  </div>
}

/** @param dirty 可核实的修改状态 @returns 未知、存在修改或干净的明确文案。 */
function worktreeLabel(dirty: boolean | null): string {
  if (dirty === null) return '工作区修改状态未知'
  return dirty ? '工作区有未提交修改' : '工作区干净'
}
