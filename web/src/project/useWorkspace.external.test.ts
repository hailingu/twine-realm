/** 远端通知不能结清未知保存，也不能把未编辑位置误当作用户草稿。 */
import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it } from 'vitest'
import type { GraphSnapshot } from '../contract/generated'
import { RequestError } from './api'
import { useWorkspace } from './useWorkspace'
import { EMPTY_GRAPH, INITIAL_PROJECT, fakeAPI, canvasReceipt } from '../testing/projectFixtures'

const serviceId = INITIAL_PROJECT.nodes[0]?.id ?? ''
/** @param external 测试注入的远端图 @returns 保留可空联合的rerender参数。 */
function externalProps(external: GraphSnapshot | null): { external: GraphSnapshot | null } { return { external } }

it('状态通知保留原未知请求，只有HTTP原键回执能确认并清掉自己的远端提示', async () => {
  const api = fakeAPI(); api.layout.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  const { result, rerender } = renderHook(({ external }: { external: GraphSnapshot | null }) => useWorkspace(INITIAL_PROJECT, api, undefined, EMPTY_GRAPH, external, true), { initialProps: externalProps(null) })
  act(() => { result.current.move(serviceId, { x: 500, y: 100 }) }); act(() => { result.current.save() })
  await waitFor(() => { expect(result.current.saveState.kind).toBe('unknown') })
  const command = api.layout.mock.calls[0]?.[1]; if (!command) throw new Error('没有保存请求')
  const project = { ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => ({ ...node, position: { x: 500, y: 100 } })) }
  rerender({ external: { project, graph: EMPTY_GRAPH } })
  expect(result.current.saveState.kind).toBe('unknown'); expect(result.current.snapshot.revision).toBe('1'); expect(result.current.remote?.project.revision).toBe('2')
  act(() => { result.current.applyRemote() }); expect(result.current.snapshot.revision).toBe('1')
  api.canvasReceipt.mockResolvedValue(canvasReceipt(project, 'saveCanvasLayout', command.mutationId))
  act(() => { result.current.verify() })
  await waitFor(() => { expect(result.current.snapshot.revision).toBe('2') })
  expect(result.current.remote).toBeNull(); expect(result.current.dirty).toBe(false); expect(api.layout).toHaveBeenCalledTimes(1)
})

it('打开表单时不偷偷推进基准；显式载入时未改动位置采用远端值', () => {
  const api = fakeAPI()
  const { result, rerender } = renderHook(({ external }: { external: GraphSnapshot | null }) => useWorkspace(INITIAL_PROJECT, api, undefined, EMPTY_GRAPH, external, true), { initialProps: externalProps(null) })
  const project = { ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => ({ ...node, position: { x: 999, y: 100 } })) }
  rerender({ external: { project, graph: EMPTY_GRAPH } })
  expect(result.current.snapshot.revision).toBe('1'); expect(result.current.dirty).toBe(false)
  act(() => { result.current.applyRemote() })
  expect(result.current.snapshot.revision).toBe('2'); expect(result.current.draft[0]?.x).toBe(999); expect(result.current.dirty).toBe(false)
})

it('真正未保存的位置仍保留，干净且无表单时可以自动恢复远端图', () => {
  const api = fakeAPI()
  const { result, rerender } = renderHook(({ external }: { external: GraphSnapshot | null }) => useWorkspace(INITIAL_PROJECT, api, undefined, EMPTY_GRAPH, external), { initialProps: externalProps(null) })
  act(() => { result.current.move(serviceId, { x: 500, y: 100 }) })
  const project = { ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => ({ ...node, position: { x: 999, y: 100 } })) }
  rerender({ external: { project, graph: EMPTY_GRAPH } }); expect(result.current.snapshot.revision).toBe('1')
  act(() => { result.current.applyRemote() }); expect(result.current.draft[0]?.x).toBe(500)
  act(() => { result.current.discard() }); expect(result.current.draft[0]?.x).toBe(999)
  rerender({ external: { project: { ...project, revision: '3' }, graph: EMPTY_GRAPH } })
  expect(result.current.snapshot.revision).toBe('3')
})
