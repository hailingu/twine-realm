/** 服务构建面板区分持久受理、检查和当前候选，不将固定源码或202伪装成部署。 */
import type { BuildCandidate } from '../contract/generated'
import type { BuildAPI } from './api'
import { useBuild } from './useBuild'
import { terminalBuild } from './model'

/** @param props 独立服务会话、已保存revision和草稿保护 @returns 固定commit构建及原操作恢复入口。 */
export function BuildPanel({ api, projectId, serviceId, revision, blocked }: Readonly<{ api: BuildAPI | undefined; projectId: string; serviceId: string; revision: string; blocked: boolean }>) {
  const build = useBuild(api, projectId, serviceId, revision)
  const { snapshot, capability, submission, readError, receipt } = build.state
  const operation = snapshot?.operation
  const unsettled = !!receipt && (!snapshot || BigInt(receipt.target.generation) > BigInt(snapshot.generation) || receipt.operationId === operation?.id && BigInt(receipt.version) > BigInt(operation.version))
  const pending = submission.kind === 'sending' || submission.kind === 'unknown' || unsettled
  const busy = !!operation && (!terminalBuild(operation.state) || operation.state === 'needs_attention')
  const disabled = blocked || pending || !capability?.enabled || !!readError
  const emptyStatus = snapshot ? '尚未构建' : '尚未读取'
  const ready = !!snapshot?.currentSource.pinnedCommit && snapshot.currentSource.projectRevision === revision
  return <section className="source-panel" aria-label="固定源码构建">
    <div className="section-heading"><h3>隔离检查与构建</h3><span>本机镜像</span></div>
    {!api ? <p>构建入口未配置。</p> : <>
      <p>{capability?.message ?? '正在读取构建能力…'}</p>
      {capability && <p>工程方案：Go + net/http · {capability.profile.id} / {capability.profile.version}</p>}
      {readError && <p role="alert">读取失败：{readError}。保留上次操作，当前候选状态尚未重新核实。</p>}
      <dl><dt>已保存的固定 commit</dt><dd><code>{snapshot?.currentSource.pinnedCommit ?? '尚未固定'}</code></dd><dt>构建状态</dt><dd><output aria-label="构建状态">{operation ? stateLabel(operation.state) : emptyStatus}</output></dd></dl>
      {operation && <><p>{operation.message}</p><ol aria-label="构建检查步骤">{operation.steps.map(step => <li key={step.name}>{stepLabel(step.name)}：{stateLabel(step.state)}{step.code ? ` · ${step.code}` : ''}</li>)}</ol><small>操作 <code>{operation.id}</code> · 版本 {operation.version}</small></>}
      <div className="source-actions"><button type="button" disabled={disabled || !ready || busy} onClick={() => { build.build() }}>检查并构建固定 commit</button>{operation && busy && <button type="button" disabled={disabled || operation.state === 'cancelling'} onClick={() => { build.cancel() }}>取消并核实原构建</button>}</div>
      {!ready && <p className="field-help">先保存布局和固定 commit；项目版本改变时先载入最新保存状态。</p>}
      {blocked && <p className="field-help">先完成或还原画布草稿，再发起构建。</p>}
      {submission.kind === 'sending' && <p>正在核实原请求…</p>}
      {submission.kind === 'rejected' && <p role="alert">本次请求未受理：{submission.message}</p>}
      {submission.kind === 'unknown' && <div className="recovery" role="alert"><strong>请核实原构建请求</strong><p>{submission.message}</p><button type="button" onClick={() => { build.verify() }}>核实原构建请求</button><button type="button" onClick={() => { build.retry() }}>重试原构建请求</button><small>保持相同请求身份，不复制构建。</small></div>}
      {snapshot?.candidate && <CandidateDetails candidate={snapshot.candidate} current={snapshot.candidateCurrent && !readError} code={snapshot.candidateCode} />}
    </>}
    <p className="field-help">仅使用已提交源码，未提交修改不进入构建。构建成功仍需下一步明确部署。</p>
  </section>
}

/** @param props 已持久候选与当前有效性 @returns 可审计镜像及输入摘要，不回显工具原日志。 */
function CandidateDetails({ candidate, current, code }: Readonly<{ candidate: BuildCandidate; current: boolean; code: string }>) {
  return <div aria-label="镜像候选"><h4>{current ? '当前固定输入已通过构建' : '历史镜像候选 · 当前尚未确认可用'}</h4><p>{code}</p><dl><dt>候选</dt><dd><code>{candidate.id}</code></dd><dt>源码</dt><dd><code>{candidate.source.pinnedCommit}</code></dd><dt>镜像 manifest</dt><dd><code>{candidate.image.manifestDigest}</code></dd><dt>本地镜像内容</dt><dd><code>{candidate.image.id}</code></dd><dt>输入摘要</dt><dd><code>{candidate.inputDigest}</code></dd></dl><p>{candidate.checks.length} 项检查 · {candidate.migrations.length} 份固定迁移材料 · {candidate.environment.architecture}</p></div>
}

/** @param value 服务端固定状态 @returns 受理、运行和终态的准确文字。 */
function stateLabel(value: string): string {
  return ({ accepted: '已受理，等待执行', executing: '检查中', cancelling: '取消已受理，正在核实', succeeded: '已通过', cancelled: '已取消并收回沙箱', failed: '未通过', needs_attention: '原结果需核实' } as Record<string, string>)[value] ?? value
}

/** @param value 固定检查名 @returns 用户可理解的进度说明。 */
function stepLabel(value: string): string {
  return ({ preflight: '核实本机工具', source: '导出固定源码', documents: '核实工程规范', sandbox: '建立隔离沙箱', dependencies: '准备锁定依赖', offline: '核实沙箱断网', toolchain: '核实工具链版本', format: 'Go格式检查', module: '依赖完整性', vet: 'Go静态检查', test: 'race测试', build: '编译静态二进制', image: '构建并核实镜像摘要', complete: '完整结果确认' } as Record<string, string>)[value] ?? value
}
