/** 受控图库尺寸回执属于渲染会话；运行投影不能清空端口测量，也不能将测量当成布局提交。 */
import { act, cleanup, render } from '@testing-library/react'
import type { ReactFlowProps } from '@xyflow/react'
import { afterEach, expect, it, vi } from 'vitest'
import { Canvas } from './Canvas'
import { BOUND_GRAPH, GRAPH_SERVICE_ID } from '../testing/graphFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'

const projection = vi.hoisted<{ current?: ReactFlowProps }>(() => ({}))
vi.mock('@xyflow/react', async importOriginal => ({
  ...await importOriginal<typeof import('@xyflow/react')>(),
  ReactFlow: (props: ReactFlowProps) => { projection.current = props; return null },
  Background: () => null, Controls: () => null
}))
afterEach(cleanup)

it('保留图库测量直至当前画布卸载，尺寸不触发布局保存或改写确认图', () => {
  const onMove = vi.fn()
  const props = { project: INITIAL_PROJECT, canvas: BOUND_GRAPH, draft: [], blocked: false, connectionBlocked: false, onMove, onConnect: vi.fn(), onReconnect: vi.fn() }
  const before = JSON.stringify({ project: INITIAL_PROJECT, graph: BOUND_GRAPH })
  const view = render(<Canvas {...props} />)
  const change = projection.current?.onNodesChange
  if (!change) throw new Error('画布未接入图库回执')
  act(() => { change([{ id: GRAPH_SERVICE_ID, type: 'dimensions', dimensions: { width: 320, height: 240 } }]) })
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toEqual({ width: 320, height: 240 })
  view.rerender(<Canvas {...props} blocked project={{ ...INITIAL_PROJECT, name: '运行观测后重新投影' }} />)
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toEqual({ width: 320, height: 240 })
  expect(projection.current?.edges?.[0]?.id).toBe(BOUND_GRAPH.bindings[0]?.id)
  expect(onMove).not.toHaveBeenCalled()
  expect(JSON.stringify({ project: INITIAL_PROJECT, graph: BOUND_GRAPH })).toBe(before)
  view.unmount(); render(<Canvas {...props} />)
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toBeUndefined()
})
