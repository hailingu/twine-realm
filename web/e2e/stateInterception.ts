/** 浏览器验收读取真实完整信封，校验后原样交付，不以响应头代表完成。 */
import { parseContract } from '../src/project/api'
import type { ProjectRunStateSnapshot } from '../src/contract/generated'

/** @returns 已完成读取、归属校验及交付的完整信封。 */
export async function deliverState(response: { status(): number; json(): Promise<unknown> }, projectId: string, fulfill: () => Promise<void>): Promise<ProjectRunStateSnapshot> {
  if (response.status() !== 200) throw new Error(`完整信封HTTP失败：${response.status()}`)
  const value = parseContract<ProjectRunStateSnapshot>('ProjectRunStateSnapshot', await response.json())
  if (value.state.project.id !== projectId || value.lifecycle.projectId !== projectId) throw new Error('完整信封项目归属不一致')
  await fulfill()
  return value
}
/** UI游标源于已应用snapshot；必须严格推进并精确匹配，不接受旧DOM或SSE消息。 */
export function appliedCursor(previous: string, delivered: string, visible: string | null): boolean {
  return BigInt(delivered) > BigInt(previous) && visible === delivered
}
/** 自有清理始终执行，主失败不能被清理失败覆盖。 */
export async function withStateCleanup(action: () => Promise<void>, cleanup: () => Promise<void>): Promise<void> {
  const failures: unknown[] = []
  try { await action() } catch (error) { failures.push(error) }
  try { await cleanup() } catch (error) { failures.push(error) }
  if (failures.length > 1) throw new AggregateError(failures, '完整信封读取与自有清理均失败')
  if (failures.length === 1) throw failures[0] instanceof Error ? failures[0] : new Error('完整信封验收失败', { cause: failures[0] })
}
/** 自有测试任务超过既有期限即明确失败；迟到拒绝仍由race消费。 */
export async function boundedStateTask<T>(task: Promise<T>, milliseconds = 8000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    return await Promise.race([task, new Promise<never>((_resolve, reject) => { timer = setTimeout(() => { reject(new Error('完整信封任务未在既有期限内结束')) }, milliseconds) })])
  } finally { clearTimeout(timer) }
}
/** 交付状态与诊断分开，唯一拥有者只在未交付时尝试一次abort。 */
export async function ownStateDelivery(deliver: (markFulfilled: () => void) => Promise<string>, abort: () => Promise<void>, diagnostic: () => Promise<void> = () => Promise.resolve()): Promise<string> {
  let fulfilled = false
  try {
    const cursor = await deliver(() => { fulfilled = true })
    await diagnostic()
    return cursor
  } catch (error) {
    const primary = error instanceof Error ? error : new Error('完整信封交付失败', { cause: error })
    if (fulfilled) throw primary
    await withStateCleanup(() => Promise.reject(primary), abort)
    throw primary
  }
}
