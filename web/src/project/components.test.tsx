/** 组件验收验证用户可见接线；画布拖动和持久化由真实浏览器另行验证。 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import { App } from './App'
import { ProjectPicker } from './ProjectPicker'
import { Workspace } from './Workspace'
import { RequestError } from './api'
import { INITIAL_PROJECT, EMPTY_CANVAS, fakeAPI, receipt } from '../testing/projectFixtures'

vi.mock('./Canvas', () => ({ Canvas: () => <div aria-label="服务画布" /> }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('从项目列表打开、登记、精确改位置、保存和返回列表', async () => {
  const api = fakeAPI(); api.list.mockResolvedValue({ projects: [{ id: INITIAL_PROJECT.id, name: INITIAL_PROJECT.name, revision: '1', nodeCount: 1, updatedAt: INITIAL_PROJECT.updatedAt }] })
  const user = userEvent.setup(); render(<App api={api} />)
  await user.click(await screen.findByRole('button', { name: /本地项目/ }))
  expect(await screen.findByRole('heading', { name: '登记本地仓库' })).toBeVisible()
  await user.type(screen.getByLabelText(/Git 工作区路径/), '/allowed/next')
  await user.type(screen.getByLabelText(/服务显示名称/), '新服务')
  await user.click(screen.getByRole('button', { name: /加入画布/ }))
  await waitFor(() => { expect(api.register).toHaveBeenCalled() })
  const input = screen.getByRole('spinbutton', { name: /本地服务 X/ })
  fireEvent.change(input, { target: { value: '420' } })
  expect(screen.getByRole('status')).toHaveTextContent('未保存')
  expect(screen.getByRole('button', { name: /加入画布/ })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '保存布局' }))
  await waitFor(() => { expect(screen.getByRole('status')).toHaveTextContent('版本 2') })
  expect(input).toHaveValue(420)
  await user.click(screen.getByRole('button', { name: '返回项目列表' }))
  expect(await screen.findByRole('heading', { name: '本地项目' })).toBeVisible()
})

it('无项目时创建，响应丢失保留相同创建请求并核实', async () => {
  const api = fakeAPI(); api.create.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '未收到响应' }, 0))
  const onOpen = vi.fn(); const user = userEvent.setup(); render(<ProjectPicker api={api} onOpen={onOpen} />)
  expect(await screen.findByText('第一张画布，等你开始')).toBeVisible()
  await user.type(screen.getByLabelText('新项目名称'), '新画布')
  await user.click(screen.getByRole('button', { name: /创建项目/ }))
  expect(await screen.findByText('创建结果尚未确认')).toBeVisible()
  expect(screen.getByLabelText('新项目名称')).toHaveValue('新画布')
  const command = api.create.mock.calls[0]?.[0]; if (!command) throw new Error('创建请求缺失')
  api.receipt.mockResolvedValue(receipt(INITIAL_PROJECT, 'createProject', command.mutationId))
  await user.click(screen.getByRole('button', { name: '核实创建结果' }))
  await waitFor(() => { expect(onOpen).toHaveBeenCalledWith({ project: INITIAL_PROJECT, canvas: EMPTY_CANVAS }) })
})

it('布局失败显示恢复入口，用户核实后原草稿才结清', async () => {
  const api = fakeAPI(); api.layout.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '未收到响应' }, 0))
  const user = userEvent.setup(); render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={vi.fn()} />)
  fireEvent.change(screen.getByRole('spinbutton', { name: /本地服务 X/ }), { target: { value: '650' } })
  await user.click(screen.getByRole('button', { name: '保存布局' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('请先核实原请求')
  expect(screen.getByRole('spinbutton', { name: /本地服务 X/ })).toHaveValue(650)
  expect(screen.getByRole('button', { name: '返回项目列表' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '重试原请求' }))
  await waitFor(() => { expect(screen.getByRole('status')).toHaveTextContent('已保存到本机') })
  fireEvent.change(screen.getByRole('spinbutton', { name: /本地服务 Y/ }), { target: { value: '800' } })
  await user.click(screen.getByRole('button', { name: '还原布局' }))
  expect(screen.getByRole('spinbutton', { name: /本地服务 Y/ })).toHaveValue(100)
})

it('未保存布局离开需明确舍弃，拒绝时留在工作区', async () => {
  const api = fakeAPI(); const onClose = vi.fn(); const user = userEvent.setup(); const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  render(<Workspace initial={INITIAL_PROJECT} api={api} onClose={onClose} />)
  fireEvent.change(screen.getByRole('spinbutton', { name: /本地服务 X/ }), { target: { value: '470' } })
  await user.click(screen.getByRole('button', { name: '返回项目列表' })); expect(onClose).not.toHaveBeenCalled()
  confirm.mockReturnValue(true)
  await user.click(screen.getByRole('button', { name: '返回项目列表' })); expect(onClose).toHaveBeenCalledOnce()
})

it('列表失败可见，卸载后的读取结果不重新打开旧项目', async () => {
  const api = fakeAPI(); api.list.mockRejectedValue(new Error('数据库暂不可用'))
  const view = render(<ProjectPicker api={api} onOpen={vi.fn()} />)
  expect(await screen.findByRole('status')).toHaveTextContent('数据库暂不可用')
  view.unmount()
  await act(async () => { await Promise.resolve() })
})
