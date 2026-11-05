/** 标准组件编辑测试验证候选保留、显式保存、未知回执与混合画布接线。 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { INITIAL_PROJECT, MYSQL_NODE, canvasReceipt, fakeAPI } from '../testing/projectFixtures'
import { MYSQL_REFERENCE } from '../contract/catalog.generated'
import { Workspace } from './Workspace'
import { RequestError } from './api'

vi.mock('./Canvas', () => ({ Canvas: () => <div aria-label="服务画布" /> }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('目录中加入缺运行配置的合法 MySQL 草稿，保留已有服务', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '＋ MySQL 组件' }))
  await waitFor(() => { expect(screen.getByRole('button', { name: '确认加入 MySQL' })).toBeEnabled() })
  fireEvent.change(screen.getByLabelText('MySQL 组件名称'), { target: { value: '业务数据库' } })
  await user.click(screen.getByRole('button', { name: '确认加入 MySQL' }))
  expect(await screen.findByRole('button', { name: '配置业务数据库' })).toBeVisible()
  expect(screen.getByRole('button', { name: '查看本地服务源码版本' })).toBeVisible()
  expect(api.addMiddleware.mock.calls[0]?.[1]).toMatchObject({ definition: MYSQL_REFERENCE, displayName: '业务数据库', configuration: { charset: 'utf8mb4', memoryMiB: 512 } })
  expect(screen.getByRole('status')).toHaveTextContent('版本 2')
})

it('非法值由同包校验阻止，服务端拒绝后不丢表单输入', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.addMiddleware.mockRejectedValueOnce(new RequestError({ code: 'INVALID_CONFIGURATION', message: '请核实配置' }, 400))
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '＋ MySQL 组件' }))
  const button = screen.getByRole('button', { name: '确认加入 MySQL' })
  await waitFor(() => { expect(button).toBeEnabled() })
  fireEvent.change(screen.getByLabelText('MySQL 内存 MiB'), { target: { value: '255' } })
  await user.click(button)
  expect(await screen.findByRole('alert')).toHaveTextContent('配置无效')
  expect(api.addMiddleware).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('MySQL 内存 MiB'), { target: { value: '1024' } })
  fireEvent.change(screen.getByLabelText('MySQL 组件名称'), { target: { value: '保留候选' } })
  await user.click(button)
  expect(await screen.findByText('本次请求未保存')).toBeVisible()
  expect(screen.getByLabelText('MySQL 内存 MiB')).toHaveValue(1024)
  expect(screen.getByLabelText('MySQL 组件名称')).toHaveValue('保留候选')
})

it('未知创建结果用原画布回执确认，不生成第二个节点请求', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.addMiddleware.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '＋ MySQL 组件' }))
  await waitFor(() => { expect(screen.getByRole('button', { name: '确认加入 MySQL' })).toBeEnabled() })
  await user.click(screen.getByRole('button', { name: '确认加入 MySQL' }))
  expect(await screen.findByText('请先核实原请求')).toBeVisible()
  expect(screen.getByLabelText('MySQL 组件名称')).toHaveValue('MySQL')
  const command = api.addMiddleware.mock.calls[0]?.[1]; if (!command) throw new Error('缺少原请求')
  api.canvasReceipt.mockResolvedValue(canvasReceipt({ ...INITIAL_PROJECT, revision: '2' }, 'addMiddleware', command.mutationId, { schemaVersion: 1, middlewares: [MYSQL_NODE] }))
  await user.click(screen.getByRole('button', { name: '核实保存结果' }))
  expect(await screen.findByRole('button', { name: '配置业务数据库' })).toBeVisible()
  expect(api.addMiddleware).toHaveBeenCalledTimes(1)
  expect(api.canvasReceipt).toHaveBeenCalledWith(command.mutationId, expect.any(AbortSignal))
})

it('配置编辑沿用原组件身份，混合布局未保存时禁止提交配置', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  render(<Workspace initial={INITIAL_PROJECT} initialCanvas={{ schemaVersion: 1, middlewares: [MYSQL_NODE] }} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '配置业务数据库' }))
  await waitFor(() => { expect(screen.getByRole('button', { name: '保存 MySQL 配置' })).toBeEnabled() })
  await user.selectOptions(screen.getByLabelText('MySQL 运行方案'), 'local-compose-v1')
  fireEvent.change(screen.getByLabelText('MySQL 内存 MiB'), { target: { value: '2048' } })
  fireEvent.change(screen.getByRole('spinbutton', { name: /业务数据库 X/ }), { target: { value: '640' } })
  expect(screen.getByRole('button', { name: '保存 MySQL 配置' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '还原布局' }))
  await user.click(screen.getByRole('button', { name: '保存 MySQL 配置' }))
  await waitFor(() => { expect(api.updateMiddleware).toHaveBeenCalled() })
  expect(api.updateMiddleware.mock.calls[0]?.[1]).toBe(MYSQL_NODE.id)
  expect(api.updateMiddleware.mock.calls[0]?.[2]).toMatchObject({ configuration: { memoryMiB: 2048, runtimeProfile: 'local-compose-v1' } })
})

it('目录不可用阻止新写入并明确显示原因', async () => {
  const api = fakeAPI(); const user = userEvent.setup()
  api.catalog.mockRejectedValue(new Error('目录与当前页面校验器版本不一致'))
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '＋ MySQL 组件' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('版本不一致')
  expect(screen.getByRole('button', { name: '确认加入 MySQL' })).toBeDisabled()
})
