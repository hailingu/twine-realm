/** 源码选择必须经用户确认；失败、未知结果与刷新均不能替换候选。 */
import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { INITIAL_PROJECT, INITIAL_SOURCE, EMPTY_CANVAS, fakeAPI, sourceReceipt } from '../testing/projectFixtures'
import { Workspace } from './Workspace'
import { RequestError } from './api'
import { SourceVersionPanel } from './SourceVersion'
import { useWorkspace } from './useWorkspace'

vi.mock('./Canvas', () => ({ Canvas: () => <div aria-label="服务画布" /> }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })
const node = INITIAL_PROJECT.nodes[0]
if (!node) throw new Error('缺少服务夹具')

it('读取、选择、确认分别发生，HEAD 刷新不替换候选或固定值', async () => {
  const api = fakeAPI(); const onPin = vi.fn(); const user = userEvent.setup()
  render(<SourceVersionPanel projectId={INITIAL_PROJECT.id} revision={INITIAL_PROJECT.revision} node={node} api={api} saved={undefined} dirty={false} blocked={false} onPin={onPin} />)
  expect(await screen.findByText('分支 develop')).toBeVisible()
  expect(screen.getByTestId('pinned-commit')).toHaveTextContent('尚未固定')
  expect(screen.getByLabelText('待固定完整 commit')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: '选择当前 HEAD' }))
  expect(onPin).not.toHaveBeenCalled()
  api.source.mockResolvedValue({ ...INITIAL_SOURCE, git: { ...INITIAL_SOURCE.git, headCommit: 'b'.repeat(40), dirty: true, branch: null } })
  await user.click(screen.getByRole('button', { name: '刷新仓库状态' }))
  expect(await screen.findByText('游离 HEAD')).toBeVisible()
  expect(screen.getByLabelText('待固定完整 commit')).toHaveValue('a'.repeat(40))
  expect(screen.getByTestId('pinned-commit')).toHaveTextContent('尚未固定')
  await user.click(screen.getByRole('button', { name: '确认固定 commit' }))
  expect(onPin).toHaveBeenCalledWith('a'.repeat(40))
})

it('源码保存响应丢失使用专用原键回执，未确认前保留候选', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.pin.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '查看本地服务源码版本' }))
  await user.click(await screen.findByRole('button', { name: '选择当前 HEAD' }))
  await user.click(screen.getByRole('button', { name: '确认固定 commit' }))
  expect(await screen.findByText('请先核实原请求')).toBeVisible()
  expect(screen.getByLabelText('待固定完整 commit')).toHaveValue('a'.repeat(40))
  const command = api.pin.mock.calls[0]?.[2]; if (!command) throw new Error('未生成固定请求')
  api.sourceReceipt.mockResolvedValue(sourceReceipt(command.mutationId))
  await user.click(screen.getByRole('button', { name: '核实保存结果' }))
  await waitFor(() => { expect(screen.getByTestId('pinned-commit')).toHaveTextContent('a'.repeat(40)) })
  expect(api.sourceReceipt).toHaveBeenCalledWith(command.mutationId, expect.any(AbortSignal))
  expect(api.receipt).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '确认固定 commit' })).toBeDisabled()
})

it('刷新失败与仓库不可用明确保留记录，尚未保存布局阻止固定', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.source.mockResolvedValue({ ...INITIAL_SOURCE, version: { ...INITIAL_SOURCE.version, pinnedCommit: 'b'.repeat(40) }, git: { ...INITIAL_SOURCE.git, available: false, problem: { code: 'PATH_UNREADABLE', message: '目录不可读取' } } })
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '查看本地服务源码版本' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('已有固定记录保留')
  expect(screen.getByTestId('pinned-commit')).toHaveTextContent('b'.repeat(40))
  fireEvent.change(screen.getByLabelText('待固定完整 commit'), { target: { value: 'c'.repeat(40) } })
  fireEvent.change(screen.getByRole('spinbutton', { name: /本地服务 X/ }), { target: { value: '540' } })
  expect(screen.getByRole('button', { name: '确认固定 commit' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '还原布局' }))
  api.source.mockRejectedValue(new Error('连接失败'))
  await user.click(screen.getByRole('button', { name: '刷新仓库状态' }))
  expect(await screen.findByText(/刷新失败：连接失败/)).toBeVisible()
  expect(screen.getByLabelText('待固定完整 commit')).toHaveValue('c'.repeat(40))
})

it('版本冲突不接受旧选择；重试沿用原键且校验回执中的选择', async () => {
  const api = fakeAPI(); const commit = 'c'.repeat(40)
  api.pin.mockRejectedValueOnce(new RequestError({ code: 'REVISION_CONFLICT', message: '版本冲突' }, 409))
  const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api))
  act(() => { result.current.pin(node.id, commit) })
  await waitFor(() => { expect(result.current.saveState.kind).toBe('rejected') })
  expect(result.current.sources[node.id]).toBeUndefined()
  api.pin.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '未知' }, 0))
  act(() => { result.current.pin(node.id, commit) })
  await waitFor(() => { expect(result.current.saveState.kind).toBe('unknown') })
  const original = api.pin.mock.calls[1]?.[2]; if (!original) throw new Error('缺少请求')
  api.pin.mockResolvedValueOnce(sourceReceipt(original.mutationId, 'd'.repeat(40)))
  act(() => { result.current.retry() })
  await waitFor(() => { const value = result.current.saveState; expect(value.kind).toBe('unknown'); if (value.kind === 'unknown') expect(value.message).toContain('不匹配') })
  expect(api.pin.mock.calls[2]?.[2]).toEqual(original)
  expect(result.current.snapshot.revision).toBe('1')
  api.pin.mockResolvedValueOnce(sourceReceipt(original.mutationId, commit))
  act(() => { result.current.retry() })
  await waitFor(() => { expect(result.current.sources[node.id]?.pinnedCommit).toBe(commit) })
})

it('冲突后载入新项目版本时重新读取固定源码，保留自己的候选', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.source.mockResolvedValue({ ...INITIAL_SOURCE, version: { ...INITIAL_SOURCE.version, pinnedCommit: 'a'.repeat(40) } })
  api.pin.mockRejectedValue(new RequestError({ code: 'REVISION_CONFLICT', message: '其他窗口已固定新版本' }, 409))
  api.canvas.mockResolvedValue({ project: { ...INITIAL_PROJECT, revision: '3' }, canvas: EMPTY_CANVAS })
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '查看本地服务源码版本' }))
  await waitFor(() => { expect(screen.getByTestId('pinned-commit')).toHaveTextContent('a'.repeat(40)) })
  fireEvent.change(screen.getByLabelText('待固定完整 commit'), { target: { value: 'b'.repeat(40) } })
  await user.click(screen.getByRole('button', { name: '确认固定 commit' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('其他窗口')
  api.source.mockResolvedValue({ ...INITIAL_SOURCE, version: { ...INITIAL_SOURCE.version, projectRevision: '3', pinnedCommit: 'c'.repeat(40) } })
  await user.click(screen.getByRole('button', { name: '载入最新版本并保留我的位置' }))
  await waitFor(() => { expect(screen.getByTestId('pinned-commit')).toHaveTextContent('c'.repeat(40)) })
  expect(screen.getByLabelText('待固定完整 commit')).toHaveValue('b'.repeat(40))
})
