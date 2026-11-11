/** 每项目仅保存非敏感原命令；页面恢复不把后台观察当成回执。 */
import type { ProjectRunCommand } from '../contract/generated'
import { parseContract } from '../project/api'

/** 读取失败必须阻止覆盖可能存在的原请求。 */
export interface SavedRequest { command: ProjectRunCommand | null; error: string }
/** @param id 项目归属 @returns 隔离的会话键。 */
function key(id: string): string { return `twine-project-lifecycle:${id}` }
/** @param id 项目 @returns 经过完整机器契约校验的原命令。 */
export function readRequest(id: string): SavedRequest {
  try {
    const raw = sessionStorage.getItem(key(id))
    if (raw === null) return { command: null, error: '' }
    return { command: parseContract<ProjectRunCommand>('ProjectRunCommand', JSON.parse(raw)), error: '' }
  } catch { return { command: null, error: '原请求会话记录无法核实，请保留记录，不要创建替代请求。' } }
}
/** @param id 项目 @param command 原命令 @returns 保存失败时不发送外部请求。 */
export function saveRequest(id: string, command: ProjectRunCommand): void { sessionStorage.setItem(key(id), JSON.stringify(command)) }
/** @param id 项目 @param mutationId 原请求身份 @returns 仅删除仍属于本次已确认请求的记录。 */
export function clearRequest(id: string, mutationId: string): void {
  const saved = readRequest(id)
  if (saved.error) throw new Error(saved.error)
  if (saved.command?.mutationId === mutationId) sessionStorage.removeItem(key(id))
}
