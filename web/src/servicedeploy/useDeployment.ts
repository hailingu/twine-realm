/** 部署会话保留用户审阅计划和原请求键；卸载仅停止客户端请求。 */
import { useRef, useState } from 'react'
import type { DeployCommand, DeployPlan, DeployReceipt } from '../contract/generated'
import { RequestError } from '../project/api'
import type { BuildAPI } from '../servicebuild/api'
import { ownedPlan, ownedReceipt, type DeploymentAPI } from './api'
import { useDeploymentRead } from './useDeploymentRead'

/** 待核实提交完整保存原预览、摘要和身份，禁止失败后换键补发。 */
interface Submission { phase: 'sending' | 'unknown'; command: DeployCommand; message: string }

/** @param error 安全请求错误 @returns 展示文案。 */
function message(error: unknown): string { return error instanceof Error ? error.message : '部署结果尚未确认，请核实原请求' }

/** @param error 请求边界错误 @returns 明确没有受理，未知传输或契约失败保持原键。 */
function rejected(error: unknown): boolean { return error instanceof RequestError && (error.status >= 400 && error.status < 500 && error.failure.code !== 'RESULT_UNKNOWN' || error.failure.code === 'DEPLOY_DISABLED') }

/** @param api 部署边界 @param buildApi 候选读取 @param id 项目 @param serviceId 服务 @returns 独立会话状态和明确动作。 */
export function useDeployment(api: DeploymentAPI | undefined, buildApi: BuildAPI | undefined, id: string, serviceId: string) {
  const { snapshot, build, capability, readError, lifetime, refresh } = useDeploymentRead(api, buildApi, id, serviceId)
  const [actionError, setActionError] = useState('')
  const [preview, setPreview] = useState<DeployPlan | null>(null)
  const [submission, setSubmission] = useState<Submission | null>(null)
  const [receipt, setReceipt] = useState<DeployReceipt | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const active = useRef<DeployCommand | null>(null)
  const inFlight = useRef(false)

  const submit = async (command: DeployCommand, verify: boolean) => {
    const signal = lifetime.current?.signal
    if (!api || !signal || signal.aborted || inFlight.current) return
    inFlight.current = true; setSubmission({ phase: 'sending', command, message: '' })
    try {
      const value = ownedReceipt(verify ? await api.receipt(command.mutationId, signal) : await api.accept(id, serviceId, command, signal), id, serviceId, command)
      if (!signal.aborted) { setReceipt(value); setSubmission(null); setPreview(null); active.current = null; if (refresh.current) void refresh.current() }
    } catch (error) {
      if (!signal.aborted) {
        if (!verify && rejected(error)) { active.current = null; setSubmission(null); setPreview(null); setActionError(message(error)) }
        else setSubmission({ phase: 'unknown', command, message: message(error) })
      }
    } finally { if (lifetime.current?.signal === signal) inFlight.current = false }
  }
  return {
    snapshot, build, capability, readError, actionError, preview, submission, receipt, previewing,
    clearPreview() { if (!active.current && !inFlight.current) { setPreview(null); setActionError('') } },
    async review(revision: string, bindingId: string, hostPort: number) {
      const signal = lifetime.current?.signal
      if (!api || !signal || signal.aborted || inFlight.current || active.current || !snapshot || !build?.candidateCurrent || !build.candidate || !capability?.enabled || readError) return
      inFlight.current = true; setPreviewing(true); setActionError(''); setPreview(null)
      const command = { baseRevision: revision, baseGeneration: snapshot.generation, candidateId: build.candidate.id, bindingId, hostPort, configuration: {} }
      try { const value = ownedPlan(await api.preview(id, serviceId, command, signal), id, serviceId, command); if (!signal.aborted) setPreview(value) }
      catch (error) { if (!signal.aborted) setActionError(message(error)) }
      finally {
        if (!signal.aborted) { setPreviewing(false) }
        if (lifetime.current?.signal === signal) { inFlight.current = false }
      }
    },
    confirm(revision: string) {
      if (preview?.request.baseRevision !== revision || active.current || inFlight.current || readError) return
      const command: DeployCommand = { mutationId: crypto.randomUUID(), preview: preview.request, planDigest: preview.digest, ...(snapshot?.operation ? { retryOf: snapshot.operation.id } : {}) }
      active.current = command; void submit(command, false)
    },
    verify() { if (active.current && submission?.phase === 'unknown') void submit(active.current, true) },
    retry() { if (active.current && submission?.phase === 'unknown') void submit(active.current, false) }
  }
}
