/** 统一状态读取穿过真实工作台组件，表单与连线候选不被远端通知覆盖。 */
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { StateSnapshot } from '../contract/generated'
import { Workspace } from '../project/Workspace'
import { INITIAL_PROJECT, MYSQL_NODE, fakeAPI } from '../testing/projectFixtures'
import { DECLARED_GRAPH } from '../testing/graphFixtures'
import { controlledStateAPI, stateEvent, stateSnapshot, stateStream } from '../testing/stateFixtures'

vi.mock('../project/Canvas', () => ({ Canvas: () => <div aria-label="服务画布" /> }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })

/** @param api 状态替身 @param streams 流列表 @param remote 新完整投影 @returns 完整通知与读取的受控交付。 */
async function deliverRemote(api: ReturnType<typeof controlledStateAPI>['api'], streams: ReturnType<typeof controlledStateAPI>['streams'], remote: StateSnapshot) {
  api.snapshot.mockResolvedValue(remote)
  await act(async () => { stateStream(streams).emit({ kind: 'state', value: stateEvent(remote.cursor) }); stateStream(streams).emit({ kind: 'caughtUp', value: { schemaVersion: 1, projectId: INITIAL_PROJECT.id, cursor: remote.cursor } }); await Promise.resolve() })
}

it('打开的MySQL配置保留输入与原基准，比较并显式载入后仍保留候选', async () => {
  const { api: stateApi, streams } = controlledStateAPI(); stateApi.snapshot.mockResolvedValue({ ...stateSnapshot(), graph: DECLARED_GRAPH })
  const api = fakeAPI(); const user = userEvent.setup(); vi.spyOn(window, 'confirm').mockReturnValue(true)
  render(<Workspace initial={INITIAL_PROJECT} initialGraph={DECLARED_GRAPH} api={api} stateApi={stateApi} onClose={() => { throw new Error('不应退出') }} />)
  await waitFor(() => { expect(streams).toHaveLength(1) })
  await user.click(screen.getByRole('button', { name: '配置业务数据库' }))
  fireEvent.change(screen.getByLabelText('MySQL 内存 MiB'), { target: { value: '768' } })
  const remote = { ...stateSnapshot('2', '2'), graph: { ...DECLARED_GRAPH, middlewares: [{ ...MYSQL_NODE, displayName: '远端数据库', configuration: { ...MYSQL_NODE.configuration, memoryMiB: 1024 } }] } }
  await deliverRemote(stateApi, streams, remote)
  expect(await screen.findByLabelText('远端画布变更')).toBeVisible()
  expect(screen.getByRole('status', { name: '画布保存状态' })).toHaveTextContent('版本 1')
  expect(screen.getByLabelText('MySQL 内存 MiB')).toHaveValue(768); expect(api.updateMiddleware).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '查看远端声明' })); expect(screen.getByLabelText('远端已保存声明')).toHaveTextContent('1024')
  await user.click(screen.getByRole('button', { name: '载入远端版本并保留我的草稿' }))
  expect(screen.getByRole('status', { name: '画布保存状态' })).toHaveTextContent('版本 2')
  expect(screen.getByLabelText('MySQL 内存 MiB')).toHaveValue(768)
  expect(screen.getByLabelText('MySQL 组件名称')).toHaveValue('业务数据库')
})

it('连线候选在重复通知和显式恢复后保持输入，通知不发出编辑命令', async () => {
  const { api: stateApi, streams } = controlledStateAPI(); stateApi.snapshot.mockResolvedValue({ ...stateSnapshot(), graph: DECLARED_GRAPH })
  const api = fakeAPI(); const user = userEvent.setup(); vi.spyOn(window, 'confirm').mockReturnValue(true)
  render(<Workspace initial={INITIAL_PROJECT} initialGraph={DECLARED_GRAPH} api={api} stateApi={stateApi} onClose={() => { throw new Error('不应退出') }} />)
  await waitFor(() => { expect(streams).toHaveLength(1) })
  await user.click(screen.getByRole('button', { name: /^建立数据库依赖$/ }))
  await user.selectOptions(screen.getByLabelText('依赖消费服务'), INITIAL_PROJECT.nodes[0]?.id ?? '')
  await user.selectOptions(screen.getByLabelText('依赖消费端口'), 'database')
  await user.selectOptions(screen.getByLabelText('依赖提供 MySQL'), MYSQL_NODE.id)
  await user.selectOptions(screen.getByLabelText('依赖提供端口'), 'mysql')
  await user.clear(screen.getByLabelText('依赖数据库名称')); await user.type(screen.getByLabelText('依赖数据库名称'), 'my_unsaved_database')
  await deliverRemote(stateApi, streams, { ...stateSnapshot('2', '2'), graph: DECLARED_GRAPH })
  act(() => { stateStream(streams).emit({ kind: 'state', value: stateEvent('2') }) })
  expect(stateApi.snapshot).toHaveBeenCalledTimes(2)
  await user.click(screen.getByRole('button', { name: '载入远端版本并保留我的草稿' }))
  expect(screen.getByLabelText('依赖数据库名称')).toHaveValue('my_unsaved_database')
  expect(screen.getByLabelText('依赖消费端口')).toHaveValue('database'); expect(api.editGraph).not.toHaveBeenCalled()
})
