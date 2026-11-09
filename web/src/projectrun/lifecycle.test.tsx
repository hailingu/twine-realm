/** 父请求恢复独立于状态信封，原键重试不创建新目标。 */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { LifecyclePanel } from './LifecyclePanel'
import { ProjectionContext } from '../projectstate/Projection'
import { stateSnapshot } from '../testing/stateFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'
import type { LifecycleAPI } from './api'
import { ownedLifecycleState, type LifecycleState } from './model'

/** @returns 没有父目标的真实内部投影。 */
function initial(): LifecycleState { return { ...stateSnapshot(), lifecycle: { schemaVersion: 1, projectId: INITIAL_PROJECT.id, generation: '0', target: null, operation: null, facts: null } } }
afterEach(() => { cleanup(); vi.restoreAllMocks(); sessionStorage.clear() })

it('完整状态拒绝跨项目父状态并保留旧严格契约', () => {
  expect(ownedLifecycleState(initial(), INITIAL_PROJECT.id).lifecycle?.generation).toBe('0')
  const value = initial()
  if (!value.lifecycle) throw new Error('缺少测试父状态')
  value.lifecycle.projectId = '其他项目'
  expect(() => ownedLifecycleState(value, INITIAL_PROJECT.id)).toThrow()
})

it('响应丢失后状态变化不能确认请求，核实失败保留原键且重试完全相同命令', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const api: LifecycleAPI = { retry: vi.fn().mockRejectedValue(new Error('未使用')), accept: vi.fn().mockRejectedValue(new Error('响应丢失')), receipt: vi.fn().mockRejectedValue(new Error('尚未找到')) }
  const view = (value: LifecycleState) => <ProjectionContext.Provider value={{ snapshot: value, managed: true, error: '', now: Date.now() }}><LifecyclePanel id={INITIAL_PROJECT.id} revision={INITIAL_PROJECT.revision} blocked={false} api={api} /></ProjectionContext.Provider>
  const rendered = render(view(initial()))
  fireEvent.click(screen.getByRole('button', { name: '停止项目并保留数据' }))
  await screen.findByRole('button', { name: '核实原项目请求' })
  expect(api.accept).toHaveBeenCalledTimes(1)
  rendered.rerender(view({ ...initial(), cursor: '2' }))
  expect(screen.getByRole('button', { name: '停止项目并保留数据' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: '核实原项目请求' }))
  await waitFor(() => { expect(api.receipt).toHaveBeenCalledTimes(1) })
  await screen.findByRole('button', { name: '重试原项目请求' })
  fireEvent.click(screen.getByRole('button', { name: '重试原项目请求' }))
  await waitFor(() => { expect(api.accept).toHaveBeenCalledTimes(2) })
  const calls = vi.mocked(api.accept).mock.calls
  expect(calls[1]).toEqual(calls[0])
  expect(vi.mocked(api.receipt).mock.calls[0]).toEqual([INITIAL_PROJECT.id, calls[0]?.[1].mutationId])
})

it('页面重新挂载恢复原键，另一项目不消费或删除原请求', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const api: LifecycleAPI = { retry: vi.fn(), accept: vi.fn().mockRejectedValue(new Error('响应丢失')), receipt: vi.fn().mockRejectedValue(new Error('未知')) }
  const view = (id: string) => <ProjectionContext.Provider value={{ snapshot: initial(), managed: true, error: '', now: Date.now() }}><LifecyclePanel key={id} id={id} revision="1" blocked={false} api={api} /></ProjectionContext.Provider>
  const first = render(view(INITIAL_PROJECT.id))
  fireEvent.click(screen.getByRole('button', { name: '停止项目并保留数据' }))
  await screen.findByRole('button', { name: '核实原项目请求' })
  const command = vi.mocked(api.accept).mock.calls[0]?.[1]
  expect(command).toBeDefined()
  first.unmount()
  const second = render(view('00000000-0000-4000-8000-000000000099'))
  expect(screen.queryByRole('button', { name: '核实原项目请求' })).not.toBeInTheDocument()
  second.rerender(view(INITIAL_PROJECT.id))
  fireEvent.click(screen.getByRole('button', { name: '核实原项目请求' }))
  await waitFor(() => { expect(api.receipt).toHaveBeenCalledWith(INITIAL_PROJECT.id, command?.mutationId) })
})

it('保存原键失败不发送命令，损坏记录阻止新目标', () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const api: LifecycleAPI = { retry: vi.fn(), accept: vi.fn(), receipt: vi.fn() }
  const view = () => <ProjectionContext.Provider value={{ snapshot: initial(), managed: true, error: '', now: Date.now() }}><LifecyclePanel id={INITIAL_PROJECT.id} revision="1" blocked={false} api={api} /></ProjectionContext.Provider>
  const mounted = render(view())
  const storage = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('配额限制') })
  fireEvent.click(screen.getByRole('button', { name: '停止项目并保留数据' }))
  expect(screen.getByText('无法保存原请求，未发送命令。')).toBeVisible()
  expect(api.accept).not.toHaveBeenCalled()
  mounted.unmount(); storage.mockRestore()
  sessionStorage.setItem(`twine-project-lifecycle:${INITIAL_PROJECT.id}`, '{}')
  render(view())
  expect(screen.getByRole('button', { name: '停止项目并保留数据' })).toBeDisabled()
  expect(screen.getByText('原请求会话记录无法核实，请保留记录，不要创建替代请求。')).toBeVisible()
})
