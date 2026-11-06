/** 受控图库尺寸回执属于渲染会话；运行投影不能清空端口测量，也不能将测量当成布局提交。 */
import { act, cleanup, render } from '@testing-library/react'
import type { ReactFlowProps } from '@xyflow/react'
import { afterEach, expect, it, vi } from 'vitest'
import { Canvas } from './Canvas'
import { BOUND_GRAPH, GRAPH_SERVICE_ID } from '../testing/graphFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'

const projection = vi.hoisted<{ current?: ReactFlowProps; ready: boolean; update: ReturnType<typeof vi.fn> }>(() => ({ ready: false, update: vi.fn() }))
vi.mock('@xyflow/react', async importOriginal => ({
  ...await importOriginal<typeof import('@xyflow/react')>(),
  ReactFlow: (props: ReactFlowProps) => { projection.current = props; return <div>{props.children}</div> },
  useNodesInitialized: () => projection.ready, useUpdateNodeInternals: () => projection.update,
  Background: () => null, Controls: () => null
}))
afterEach(() => { cleanup(); projection.ready = false; projection.update.mockClear() })

it('保留图库测量直至当前画布卸载，尺寸不触发布局保存或改写确认图', () => {
  const onMove = vi.fn()
  const props = { project: INITIAL_PROJECT, canvas: BOUND_GRAPH, draft: [], blocked: false, connectionBlocked: false, onMove, onConnect: vi.fn(), onReconnect: vi.fn() }
  const before = JSON.stringify({ project: INITIAL_PROJECT, graph: BOUND_GRAPH })
  const view = render(<Canvas {...props} />)
  expect(projection.update).not.toHaveBeenCalled()
  const change = projection.current?.onNodesChange
  if (!change) throw new Error('画布未接入图库回执')
  projection.ready = true
  act(() => { change([{ id: GRAPH_SERVICE_ID, type: 'dimensions', dimensions: { width: 320, height: 240 } }]) })
  expect(projection.update).toHaveBeenCalledWith([GRAPH_SERVICE_ID, ...BOUND_GRAPH.middlewares.map(node => node.id)])
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toEqual({ width: 320, height: 240 })
  view.rerender(<Canvas {...props} blocked project={{ ...INITIAL_PROJECT, name: '运行观测后重新投影' }} />)
  expect(projection.update).toHaveBeenCalledTimes(1)
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toEqual({ width: 320, height: 240 })
  expect(projection.current?.edges?.[0]?.id).toBe(BOUND_GRAPH.bindings[0]?.id)
  expect(onMove).not.toHaveBeenCalled()
  expect(JSON.stringify({ project: INITIAL_PROJECT, graph: BOUND_GRAPH })).toBe(before)
  view.unmount(); projection.ready = false; render(<Canvas {...props} />)
  expect(projection.current?.nodes?.find(node => node.id === GRAPH_SERVICE_ID)?.measured).toBeUndefined()
})
