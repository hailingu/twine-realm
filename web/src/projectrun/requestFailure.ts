/** 请求失败按确定拒绝与未知分流，不能因本地清理失败丢弃恢复身份。 */
import { RequestError } from '../project/api'
import { clearRequest } from './requestMemory'

/** 失败结果只交给仍拥有请求的页面会话。 */
export interface SubmissionFailure { phase: 'rejected' | 'unknown'; message: string; cleared: boolean }
/** @param error 运输或协议错误 @param verify 是否只读核实 @param id 项目 @param mutationId 原键 @returns 安全反馈及原请求清理事实。 */
export function submissionFailure(error: unknown, verify: boolean, id: string, mutationId: string): SubmissionFailure {
  const rejected = !verify && error instanceof RequestError && error.status >= 400 && error.status < 500 && error.failure.code !== 'RESULT_UNKNOWN'
  if (rejected) {
    try { clearRequest(id, mutationId) }
    catch { return { phase: 'unknown', message: '无法清除原请求记录，请保留并核实。', cleared: false } }
  }
  return { phase: rejected ? 'rejected' : 'unknown', message: error instanceof RequestError ? error.message : '请求结果尚未确认，请核实或重试原请求。', cleared: rejected }
}
