/** 运行详情行为测试区分声明、受理、事实和明确确认，错误仍保留恢复入口。 */
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { MiddlewareNode } from '../contract/generated'
import { INITIAL_PROJECT, MYSQL_NODE } from '../testing/projectFixtures'
import { READY_RUNTIME, RUNTIME_CAPABILITY, RUNTIME_COMMAND, RUNTIME_RECEIPT, fakeRuntimeAPI } from '../testing/runtimeFixtures'
import { RequestError } from '../project/api'
import { initialRuntime, type RuntimeState } from './model'
import { RuntimePanel } from './RuntimePanel'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

/** @param state 显式受理与事实输入 @param node 已保存声明 @param blocked 布局保护 @returns 详情端口与事件测试引用。 */
function panel(state: RuntimeState = { ...initialRuntime(), capability: RUNTIME_CAPABILITY, snapshot: READY_RUNTIME, now: Date.parse('2026-11-05T09:00:01Z') }, node: MiddlewareNode = { ...MYSQL_NODE, configuration: { ...RUNTIME_RECEIPT.target.resource.configuration }, notReadyReasons: [] }, blocked = false) {
  const api = fakeRuntimeAPI()
  const runtime = { state, run: vi.fn(), verify: vi.fn(), retry: vi.fn() }
  const view = render(<RuntimePanel projectId={INITIAL_PROJECT.id} revision="5" node={node} runtime={runtime} api={api} blocked={blocked} />)
  return { ...view, api, runtime }
}

it('明确确认后才发出运行或停止意图，真实事实和操作进度分别呈现', async () => {
  const user = userEvent.setup()
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValue(true)
  const { runtime } = panel()
  expect(screen.getByRole('status', { name: 'MySQL运行事实' })).toHaveTextContent('认证就绪')
  expect(screen.getByTestId('runtime-operation-state')).toHaveTextContent('本次操作已核实')
  await user.click(screen.getByRole('button', { name: '重新核实运行目标' }))
  expect(runtime.run).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '重新核实运行目标' }))
  expect(runtime.run).toHaveBeenLastCalledWith(MYSQL_NODE.id, '5', 'running')
  await user.click(screen.getByRole('button', { name: '确认停止 MySQL' }))
  expect(runtime.run).toHaveBeenLastCalledWith(MYSQL_NODE.id, '5', 'stopped')
  expect(confirm).toHaveBeenCalledTimes(3)
})

it('声明配置变更阻止另起实例，停止仍指向原目标；布局未保存阻止运行写入', () => {
  const state = { ...initialRuntime(), capability: RUNTIME_CAPABILITY, snapshot: READY_RUNTIME }
  const node = { ...MYSQL_NODE, configuration: { ...RUNTIME_RECEIPT.target.resource.configuration, memoryMiB: 1024 }, notReadyReasons: [] }
  const { unmount } = panel(state, node)
  expect(screen.getByRole('alert')).toHaveTextContent('固定运行配置不同')
  expect(screen.getByRole('button', { name: '重新核实运行目标' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '确认停止 MySQL' })).toBeEnabled()
  unmount(); panel(state, node, true)
  expect(screen.getByRole('button', { name: '确认停止 MySQL' })).toBeDisabled()
})

it('未知原请求提供核实与原键重试，已知拒绝和读取失败各自说明，不抹掉最后事实', async () => {
  const user = userEvent.setup()
  const state: RuntimeState = { ...initialRuntime(), capability: RUNTIME_CAPABILITY, snapshot: READY_RUNTIME, readError: '采集连接断开', submission: { kind: 'unknown', command: RUNTIME_COMMAND, message: '响应丢失', verifying: false } }
  const { runtime, unmount } = panel(state)
  expect(screen.getByRole('status', { name: 'MySQL运行事实' })).toHaveTextContent('未知')
  await user.click(screen.getByRole('button', { name: '核实原运行请求' }))
  await user.click(screen.getByRole('button', { name: '重试原运行请求' }))
  expect(runtime.verify).toHaveBeenCalledOnce(); expect(runtime.retry).toHaveBeenCalledOnce()
  unmount(); panel({ ...state, submission: { kind: 'rejected', id: RUNTIME_COMMAND.mutationId, message: '版本冲突' } })
  expect(screen.getByText('目标未受理：版本冲突')).toBeVisible()
})

it('事件失败可重试，明确翻页使用服务端游标，不将事件读取当成状态更新', async () => {
  const user = userEvent.setup()
  const { api } = panel()
  api.events.mockRejectedValueOnce(new RequestError({ code: 'DATABASE_UNAVAILABLE', message: '事件尚未读取' }, 503))
  await user.click(screen.getByText('持久运行事件', { exact: true }))
  await user.click(screen.getByRole('button', { name: '读取运行事件' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('事件尚未读取')
  api.events.mockResolvedValueOnce({ schemaVersion: 1, events: [{ sequence: '100', operationId: RUNTIME_RECEIPT.operationId, generation: '1', kind: 'observed', code: 'READY', createdAt: '2026-11-05T09:00:00Z' }], nextSequence: '100', hasMore: true })
  await user.click(screen.getByRole('button', { name: '读取运行事件' }))
  await user.click(await screen.findByRole('button', { name: '读取下一页事件' }))
  await waitFor(() => { expect(api.events).toHaveBeenLastCalledWith(INITIAL_PROJECT.id, '100', expect.any(AbortSignal)) })
  expect(screen.getByRole('status', { name: 'MySQL运行事实' })).toHaveTextContent('认证就绪')
})
