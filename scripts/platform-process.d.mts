/** 测试平台子进程收回端口，不管理业务Docker资源。 */
import type { ChildProcess } from 'node:child_process'
export function stopPlatformProcess(child: ChildProcess | undefined, options?: { timeoutMs?: number; killTimeoutMs?: number }): Promise<void>
export function startOwnedPlatform<T>(start: () => Promise<T>, stop: () => Promise<void>): Promise<T>
export function isProcessExitUnconfirmed(error: unknown): boolean
/** 收回进程后只在已确认退出时清理，并保留所有验收与清理错误。 */
export function finishOwnedPlatform(failures: unknown[], stop: () => Promise<void>, cleanup: () => Promise<void>): Promise<void>
