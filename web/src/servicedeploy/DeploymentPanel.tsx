/** 部署面板先审阅固定候选、数据库绑定与入口，再明确接受持久目标。 */
import { useState } from 'react'
import type { DatabaseBinding, DeployPlan } from '../contract/generated'
import type { BuildAPI } from '../servicebuild/api'
import type { DeploymentAPI } from './api'
import { terminalDeployment } from './model'
import { useDeployment } from './useDeployment'

/** @param props 已保存声明与独立服务会话 @returns 部署审阅、进度、原键恢复和真实业务入口。 */
export function DeploymentPanel({ api, buildApi, projectId, serviceId, revision, bindings, blocked }: Readonly<{ api: DeploymentAPI | undefined; buildApi: BuildAPI | undefined; projectId: string; serviceId: string; revision: string; bindings: DatabaseBinding[]; blocked: boolean }>) {
  const deployment = useDeployment(api, buildApi, projectId, serviceId)
  const [port, setPort] = useState('8081')
  const { snapshot, capability, preview, receipt, submission, readError } = deployment
  const binding = bindings.filter(value => value.source.componentId === serviceId)
  const operation = snapshot?.operation
  const pending = !!submission || deployment.previewing || !!receipt && (!snapshot || BigInt(receipt.target.generation) > BigInt(snapshot.generation))
  const busy = !!operation && !terminalDeployment(operation.state)
  const disabled = blocked || pending || busy || !capability?.enabled || !!readError
  const portNumber = Number(port)
  const ready = snapshot?.freshness === 'fresh' && !readError && snapshot.observation?.ready
  const reviewCurrent = preview?.request.baseRevision === revision
  return <section className="source-panel" aria-label="单服务MySQL部署">
    <div className="section-heading"><h3>部署到本机</h3><span>服务 + MySQL</span></div>
    {!api || !buildApi ? <p>部署入口未配置。</p> : <>
      <p>{capability?.message ?? '正在读取部署能力…'}</p>
      {readError && <p role="alert">读取失败：{readError}。保留上次事实，当前状态尚未重新核实。</p>}
      <label htmlFor={`deploy-port-${serviceId}`}>业务入口端口</label><input id={`deploy-port-${serviceId}`} type="number" min={1024} max={65535} step={1} value={port} disabled={disabled} onChange={event => { setPort(event.target.value); deployment.clearPreview() }} />
      <p>只绑定 127.0.0.1；数据库保持项目私网。</p>
      <button type="button" disabled={disabled || binding.length !== 1 || !deployment.build?.candidateCurrent || !Number.isInteger(portNumber) || portNumber < 1024 || portNumber > 65535} onClick={() => { const selected = binding[0]; if (selected) void deployment.review(revision, selected.id, portNumber) }}>审阅部署</button>
      {blocked && <p className="field-help">先保存或还原画布草稿，再审阅部署。</p>}
      {binding.length !== 1 && <p className="field-help">先为此服务保存一条有效的 MySQL 依赖。</p>}
      {preview && <><PlanReview plan={preview} /><button type="button" disabled={disabled || !reviewCurrent} onClick={() => { deployment.confirm(revision) }}>确认部署到本机</button>{!reviewCurrent && <p role="alert">画布版本已改变，请重新审阅。</p>}</>}
      {deployment.previewing && <p>正在只读核实候选与原数据库…</p>}
      {deployment.actionError && <p role="alert">本次请求未受理：{deployment.actionError}</p>}
      <p>部署状态：<output aria-label="部署状态">{operation ? stateLabel(operation.state) : '尚未部署'}</output></p>
      {operation && <><p>{operation.message}</p><ol aria-label="部署步骤">{operation.steps.map(step => <li key={step.name}>{stepLabel(step.name)}：{stateLabel(step.state)}{step.code ? ` · ${step.code}` : ''}</li>)}</ol><small>操作 <code>{operation.id}</code> · 版本 {operation.version}</small></>}
      {submission?.phase === 'sending' && <p>正在核实部署受理结果…</p>}
      {submission?.phase === 'unknown' && <div className="recovery" role="alert"><strong>请核实原部署请求</strong><p>{submission.message}</p><button type="button" onClick={() => { deployment.verify() }}>核实原部署请求</button><button type="button" onClick={() => { deployment.retry() }}>重试原部署请求</button><small>保持原计划和相同请求身份。</small></div>}
      {snapshot?.observation && <p>运行 {snapshot.observation.running ? '是' : '否'} · 存活 {snapshot.observation.healthy ? '是' : '否'} · 就绪 {ready ? '已确认' : '未确认'} · {snapshot.freshness === 'fresh' && !readError ? '近期采集' : '状态需核实'}</p>}
      {snapshot?.observationCode && <p role="alert">当前采集：{snapshot.observationCode}。上次事实保留。</p>}
      {ready && snapshot.observation && <a className="primary" href={snapshot.observation.entryUrl} target="_blank" rel="noopener noreferrer">打开业务入口 ↗</a>}
    </>}
    <p className="field-help">部署受理后由后台继续执行。失败后保留原数据、任务与凭据；不会自动换库、换端口或清除迁移错误。</p>
  </section>
}

/** @param props 已核实的精确预览 @returns 用户确认前可审阅的真实输入和权限边界。 */
function PlanReview({ plan }: Readonly<{ plan: DeployPlan }>) {
  return <div aria-label="部署预览"><h4>{plan.serviceName}</h4><dl><dt>固定源码</dt><dd><code>{plan.candidate.source.pinnedCommit}</code></dd><dt>已检查镜像</dt><dd><code>{plan.candidate.image.id}</code></dd><dt>数据库</dt><dd>{plan.binding.databaseName} · {plan.binding.purpose}</dd><dt>迁移材料</dt><dd>{plan.candidate.migrations.length} 份固定文件</dd><dt>业务入口</dt><dd>http://127.0.0.1:{plan.request.hostPort}</dd></dl><p>先迁移，再启动应用。应用获得本库业务表{plan.binding.purpose === 'readOnly' ? '读取' : '读写'}权限，迁移历史仅可读；两种用途凭据独立注入。</p></div>
}

/** @param value 固定阶段状态 @returns 不把受理或running叫作成功的状态说明。 */
function stateLabel(value: string): string { return ({ accepted: '已受理，等待执行', executing: '执行中', succeeded: '已确认完成', failed: '未完成', unknown: '结果待核实', needs_attention: '原结果需核实' } as Record<string, string>)[value] ?? value }

/** @param value 固定步骤 @returns 用户可理解的运行阶段。 */
function stepLabel(value: string): string { return ({ preflight: '核实镜像与环境', database: '准备业务库与用途账号', material: '核实运行配置', migration: '执行并核实原迁移', permissions: '核实应用最小权限', container: '启动原业务容器', readiness: '核实业务就绪' } as Record<string, string>)[value] ?? value }
