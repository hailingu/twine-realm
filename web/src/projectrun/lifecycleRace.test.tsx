/** 迟到的旧页面响应不得清除新页面原请求恢复键。 */
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ProjectRunCommand, ProjectRunReceipt } from '../contract/generated'
import { RequestError } from '../project/api'
import { ProjectionContext } from '../projectstate/Projection'
import { stateSnapshot } from '../testing/stateFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'
import { RUNTIME_RECEIPT } from '../testing/runtimeFixtures'
import { DEPLOY_RECEIPT } from '../testing/deploymentFixtures'
import { LifecyclePanel } from './LifecyclePanel'
import { clearRequest, readRequest, saveRequest } from './requestMemory'
import type { LifecycleAPI } from './api'

/** @param command 原命令 @returns 与请求一致的纯协议回执。 */
function receipt(command: ProjectRunCommand): ProjectRunReceipt {
  return { schemaVersion: 1, mutationId: command.mutationId, operationId: '00000000-0000-4000-8000-000000000088', replayed: true, target: { projectId: INITIAL_PROJECT.id, generation: '1', desired: command.desired, createdAt: '2026-11-09T00:00:00Z', frozen: { runtime: RUNTIME_RECEIPT.target, deployment: DEPLOY_RECEIPT.target, databaseContainerId: 'db', applicationContainerId: 'app', databaseNetworkId: 'network', ingressNetworkId: 'ingress', databaseVolumeCreatedAt: 'created', migrationContainerId: 'migration', serverUuid: 'uuid' } } }
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); sessionStorage.clear() })

it.each(['成功', '明确拒绝'])('旧页面%s迟到不能删除新页面请求B', async outcome => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  let resolve!: (value: ProjectRunReceipt) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<ProjectRunReceipt>((accept, decline) => { resolve = accept; reject = decline })
  const deferred = { promise, resolve, reject }
  let original: ProjectRunCommand | undefined
  const api: LifecycleAPI = { retry: vi.fn(), accept: vi.fn<LifecycleAPI['accept']>().mockImplementationOnce((_id, command) => { original = command; return deferred.promise }).mockRejectedValue(new Error('新请求结果未知')), receipt: vi.fn<LifecycleAPI['receipt']>().mockImplementation(() => { if (!original) throw new Error('缺少原请求'); return Promise.resolve(receipt(original)) }) }
  const state = { ...stateSnapshot(), lifecycle: { schemaVersion: 1 as const, projectId: INITIAL_PROJECT.id, generation: '0', target: null, operation: null, facts: null } }
  const view = () => <ProjectionContext.Provider value={{ snapshot: state, managed: true, error: '', now: Date.now() }}><LifecyclePanel id={INITIAL_PROJECT.id} revision="1" blocked={false} api={api} /></ProjectionContext.Provider>
  const first = render(view())
  fireEvent.click(screen.getByRole('button', { name: '停止项目并保留数据' }))
  first.unmount()
  render(view())
  fireEvent.click(screen.getByRole('button', { name: '核实原项目请求' }))
  await screen.findByText('原请求已受理；实际停止或运行结果以后台核实事实为准。')
  fireEvent.click(screen.getByRole('button', { name: '停止项目并保留数据' }))
  await screen.findByRole('button', { name: '核实原项目请求' })
  const newer = readRequest(INITIAL_PROJECT.id).command
  expect(newer?.mutationId).not.toBe(original?.mutationId)
  await act(async () => {
    if (!original) throw new Error('缺少原请求')
    if (outcome === '成功') deferred.resolve(receipt(original))
    else deferred.reject(new RequestError({ code: 'PROJECT_RUN_BUSY', message: '原请求明确拒绝' }, 409))
    await Promise.resolve()
  })
  expect(readRequest(INITIAL_PROJECT.id).command).toEqual(newer)
})

it('条件清除不删除不同mutationId的较新记录', () => {
  const command: ProjectRunCommand = { mutationId: crypto.randomUUID(), baseRevision: '1', baseGeneration: '0', desired: 'stopped' }
  saveRequest(INITIAL_PROJECT.id, command)
  clearRequest(INITIAL_PROJECT.id, crypto.randomUUID())
  expect(readRequest(INITIAL_PROJECT.id).command).toEqual(command)
  clearRequest(INITIAL_PROJECT.id, command.mutationId)
  expect(readRequest(INITIAL_PROJECT.id).command).toBeNull()
})
