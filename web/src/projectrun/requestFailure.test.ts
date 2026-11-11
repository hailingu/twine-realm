/** 会话存储故障与核实失败不能丢弃尚未确认的原命令。 */
import { afterEach, expect, it, vi } from 'vitest'
import type { ProjectRunCommand } from '../contract/generated'
import { RequestError } from '../project/api'
import { clearRequest, readRequest, saveRequest } from './requestMemory'
import { submissionFailure } from './requestFailure'
const id = '00000000-0000-4000-8000-000000000001'
const command: ProjectRunCommand = { mutationId: '00000000-0000-4000-8000-000000000002', baseRevision: '1', baseGeneration: '0', desired: 'stopped' }
afterEach(() => { vi.restoreAllMocks(); sessionStorage.clear() })
it('损坏或包含额外字段的会话记录拒绝恢复且不能清除', () => {
  sessionStorage.setItem(`twine-project-lifecycle:${id}`, '{')
  expect(readRequest(id).error).not.toBe('')
  expect(() => clearRequest(id, command.mutationId)).toThrow()
  sessionStorage.setItem(`twine-project-lifecycle:${id}`, JSON.stringify({ ...command, secret: '不接受额外字段' }))
  expect(readRequest(id).command).toBeNull()
})
it('存储写入异常向拥有者报告，不伪造已保存', () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('配额限制') })
  expect(() => saveRequest(id, command)).toThrow('配额限制')
})
it('明确拒绝的清理失败仍保留原请求，核实404不等于未受理', () => {
  saveRequest(id, command)
  const rejected = new RequestError({ code: 'PROJECT_RUN_BUSY', message: '尚未就绪' }, 409)
  const removal = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('存储不可用') })
  expect(submissionFailure(rejected, false, id, command.mutationId)).toMatchObject({ phase: 'unknown', cleared: false })
  expect(readRequest(id).command).toEqual(command)
  removal.mockRestore()
  expect(submissionFailure(new RequestError({ code: 'MUTATION_NOT_FOUND', message: '未找到' }, 404), true, id, command.mutationId)).toMatchObject({ phase: 'unknown', cleared: false })
  expect(readRequest(id).command).toEqual(command)
  expect(submissionFailure(rejected, false, id, command.mutationId)).toMatchObject({ phase: 'rejected', cleared: true })
  expect(readRequest(id).command).toBeNull()
})
it('传输异常和显式结果未知即使带4xx也保留原键', () => {
  saveRequest(id, command)
  expect(submissionFailure(new Error('断网'), false, id, command.mutationId).phase).toBe('unknown')
  expect(submissionFailure(new RequestError({ code: 'RESULT_UNKNOWN', message: '未知' }, 409), false, id, command.mutationId).phase).toBe('unknown')
  expect(readRequest(id).command).toEqual(command)
})
