/** 用户表单、端口候选与原请求恢复共享真实生成的校验器。 */
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { Workspace } from './Workspace'
import { ServiceContractPanel } from './ServiceContractPanel'
import { BindingPanel } from './BindingPanel'
import { RequestError } from './api'
import { bindingCandidate, missingPorts, servicePorts } from './graphModel'
import { useWorkspace } from './useWorkspace'
import { DB_BINDING, BOUND_GRAPH, DECLARED_GRAPH, GRAPH_SERVICE_ID, graphProject, serviceDocument } from '../testing/graphFixtures'
import { EMPTY_GRAPH, INITIAL_PROJECT, MYSQL_NODE, canvasReceipt, fakeAPI, graphReceipt } from '../testing/projectFixtures'

vi.mock('./Canvas', () => ({ Canvas: () => <div aria-label="服务画布" /> }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('未定义服务不臆造端口，显式声明后按 required 消费端口判断缺绑定', () => {
  expect(servicePorts(EMPTY_GRAPH, GRAPH_SERVICE_ID)).toEqual([])
  expect(missingPorts(DECLARED_GRAPH, GRAPH_SERVICE_ID)).toEqual(['database'])
  expect(missingPorts(BOUND_GRAPH, GRAPH_SERVICE_ID)).toEqual([])
})

it('服务契约不自动装载示例，非法 JSON 保留候选，显式载入后提交多接口版本', async () => {
  const node = INITIAL_PROJECT.nodes[0]; if (!node) throw new Error('缺服务夹具')
  const edit = vi.fn(); const user = userEvent.setup()
  render(<ServiceContractPanel node={node} graph={EMPTY_GRAPH} blocked={false} onEdit={edit} />)
  const input = screen.getByRole('textbox', { name: /服务契约 JSON/ })
  expect(input).toHaveValue('')
  await user.click(screen.getByRole('button', { name: '保存服务契约' }))
  expect(screen.getByRole('alert')).toHaveTextContent('请填写契约')
  fireEvent.change(input, { target: { value: '{invalid' } })
  await user.click(screen.getByRole('button', { name: '保存服务契约' }))
  expect(input).toHaveValue('{invalid'); expect(edit).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '载入端口示例' }))
  await user.click(screen.getByRole('button', { name: '保存服务契约' }))
  expect(edit).toHaveBeenCalledWith({ type: 'service.setContract', serviceId: node.id, document: serviceDocument() })
})

it('发布新服务契约时可显式映射原连接，合成单个原子批次', async () => {
  const node = INITIAL_PROJECT.nodes[0]; if (!node) throw new Error('缺服务夹具')
  const edit = vi.fn(); const user = userEvent.setup()
  render(<ServiceContractPanel node={node} graph={BOUND_GRAPH} blocked={false} onEdit={edit} />)
  const document = serviceDocument(); document.version = '1.1.0'; const port = document.ports.find(value => value.id === 'database'); if (!port) throw new Error('缺端口夹具'); port.id = 'storage'
  fireEvent.change(screen.getByRole('textbox', { name: /服务契约 JSON/ }), { target: { value: JSON.stringify(document) } })
  await user.click(screen.getByRole('checkbox', { name: '同时重新确认现有数据库连接' }))
  await user.selectOptions(screen.getByLabelText('连接app_local的新端口'), 'storage')
  await user.click(screen.getByRole('button', { name: '保存服务契约' }))
  expect(edit).toHaveBeenCalledWith({ type: 'service.setContract', serviceId: node.id, document }, expect.objectContaining({ type: 'binding.reconnect', bindingId: DB_BINDING.id, source: { componentId: node.id, portId: 'storage' }, databaseName: DB_BINDING.databaseName }))
})

it('依赖表单拒绝缺端点和非法库名，重连保留绑定身份且候选不改确认图', async () => {
  const edit = vi.fn(); const user = userEvent.setup()
  render(<BindingPanel project={INITIAL_PROJECT} graph={DECLARED_GRAPH} candidate={bindingCandidate(DB_BINDING.source, DB_BINDING.target, DB_BINDING)} blocked={false} onEdit={edit} />)
  await user.clear(screen.getByLabelText('依赖数据库名称')); await user.type(screen.getByLabelText('依赖数据库名称'), 'Bad-Name')
  await user.click(screen.getByRole('button', { name: '确认重连依赖' }))
  expect(screen.getByRole('alert')).toHaveTextContent('数据库名称'); expect(edit).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('依赖数据库名称'), { target: { value: 'app_new' } })
  await user.selectOptions(screen.getByLabelText('依赖数据库用途'), 'readOnly')
  await user.click(screen.getByRole('button', { name: '确认重连依赖' }))
  expect(edit).toHaveBeenCalledWith(expect.objectContaining({ type: 'binding.reconnect', bindingId: DB_BINDING.id, databaseName: 'app_new', purpose: 'readOnly' }))
  expect(DECLARED_GRAPH.bindings).toEqual([])
})

it('图请求响应丢失后核实原键，重试不生成新身份，错误回执不能覆盖已保存关系', async () => {
  const api = fakeAPI(); api.editGraph.mockRejectedValue(new RequestError({ code: 'TRANSPORT_FAILED', message: '未知' }, 0))
  const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api, undefined, BOUND_GRAPH))
  await act(async () => { result.current.editGraph({ type: 'binding.remove', bindingId: DB_BINDING.id }); await Promise.resolve() })
  expect(result.current.blocked).toBe(true); expect(result.current.canvas.bindings).toEqual([DB_BINDING])
  const command = api.editGraph.mock.calls[0]?.[1]; if (!command) throw new Error('缺原请求')
  await act(async () => { result.current.retry(); await Promise.resolve() })
  expect(api.editGraph.mock.calls[1]?.[1]).toEqual(command)
  api.graphReceipt.mockResolvedValueOnce(graphReceipt(graphProject(), crypto.randomUUID()))
  await act(async () => { result.current.verify(); await Promise.resolve() })
  expect(result.current.blocked).toBe(true); expect(result.current.canvas.bindings).toEqual([DB_BINDING])
  api.graphReceipt.mockResolvedValueOnce(graphReceipt(graphProject(), command.mutationId, DECLARED_GRAPH))
  await act(async () => { result.current.verify(); await Promise.resolve() })
  expect(api.graphReceipt).toHaveBeenLastCalledWith(command.mutationId, expect.any(AbortSignal))
  expect(result.current.canvas.bindings).toEqual([]); expect(result.current.blocked).toBe(false)
})

it('旧类配置回执保留连接，版本冲突重载完整图时保留本地连接候选', async () => {
  const api = fakeAPI(); api.editGraph.mockRejectedValueOnce(new RequestError({ code: 'REVISION_CONFLICT', message: '版本冲突' }, 409))
  api.graph.mockResolvedValue({ project: graphProject('4'), graph: BOUND_GRAPH })
  const user = userEvent.setup(); render(<Workspace initial={INITIAL_PROJECT} initialGraph={DECLARED_GRAPH} api={api} onClose={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: '建立数据库依赖' }))
  await user.selectOptions(screen.getByLabelText('依赖消费服务'), GRAPH_SERVICE_ID)
  await user.selectOptions(screen.getByLabelText('依赖消费端口'), 'database')
  await user.selectOptions(screen.getByLabelText('依赖提供 MySQL'), MYSQL_NODE.id)
  await user.selectOptions(screen.getByLabelText('依赖提供端口'), 'mysql')
  fireEvent.change(screen.getByLabelText('依赖数据库名称'), { target: { value: 'app_candidate' } })
  await user.click(screen.getByRole('button', { name: '确认保存依赖' }))
  expect(await screen.findByText('本次请求未保存')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '载入最新版本并保留我的位置' }))
  await waitFor(() => { expect(screen.getByRole('status')).toHaveTextContent('版本 4') })
  expect(screen.getByLabelText('依赖数据库名称')).toHaveValue('app_candidate')
  expect(screen.getByLabelText('已保存数据库依赖')).toHaveTextContent('app_local')
  const command = api.editGraph.mock.calls[0]?.[1]; expect(command?.baseRevision).toBe('1')
})

it('更新旧类中间件配置只覆盖中间件集合，已确认服务契约和绑定保持原值', async () => {
  const api = fakeAPI()
  api.updateMiddleware.mockImplementation((_id, _componentId, command) => Promise.resolve(canvasReceipt(graphProject(), 'updateMiddleware', command.mutationId, { schemaVersion: 1, middlewares: [{ ...MYSQL_NODE, displayName: '新名字' }] })))
  const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api, undefined, BOUND_GRAPH))
  await act(async () => { result.current.middleware(MYSQL_NODE.id, { definition: MYSQL_NODE.definition, displayName: '新名字', configuration: MYSQL_NODE.configuration, position: MYSQL_NODE.position }); await Promise.resolve() })
  expect(result.current.canvas.bindings).toEqual([DB_BINDING])
  expect(result.current.canvas.serviceContracts).toEqual(BOUND_GRAPH.serviceContracts)
  expect(result.current.canvas.middlewares[0]?.displayName).toBe('新名字')
})
