/** 前端构建把所有 Vite / 打包器告警升级为失败，保留完整可操作诊断。 */
import { createLogger } from 'vite'
import type { Logger } from 'vite'

/** @returns 不允许 warn / warnOnce 继续构建的日志边界。 */
export function createWarningFreeLogger(): Logger {
  const logger = createLogger()
  const rejectWarning = (message: string): never => { throw new Error(`构建告警必须修复：${message}`) }
  return { ...logger, warn: rejectWarning, warnOnce: rejectWarning }
}
