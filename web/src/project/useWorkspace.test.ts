/** 会话行为测试覆盖失败保留、原键重试、冲突恢复与旧结果隔离。 */
import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { CanvasReceipt } from '../contract/generated'
import { RequestError } from './api'
import { fakeAPI, INITIAL_PROJECT, EMPTY_GRAPH, canvasReceipt } from '../testing/projectFixtures'
import { useWorkspace } from './useWorkspace'

const serviceId = INITIAL_PROJECT.nodes[0]?.id ?? ''
describe('工作区保存状态机', () => {
  it('未知结果保留草稿，原请求重试后只有确认结果覆盖快照', async () => {
    const api = fakeAPI(); api.layout.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
    const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api))
    act(() => { result.current.move(serviceId, { x: 540, y: 120 }) })
    act(() => { result.current.save(); result.current.save() })
    await waitFor(() => { expect(result.current.saveState.kind).toBe('unknown') })
    expect(result.current.draft[0]?.x).toBe(540)
    expect(result.current.snapshot.revision).toBe('1')
    expect(api.layout).toHaveBeenCalledTimes(1)
    const original = api.layout.mock.calls[0]?.[1]
    act(() => { result.current.retry() })
    await waitFor(() => { expect(result.current.snapshot.revision).toBe('2') })
    expect(api.layout.mock.calls[1]?.[1]).toEqual(original)
    expect(result.current.dirty).toBe(false)
  })
  it('核实未受理仍保留原请求，找到回执才确认保存', async () => {
    const api = fakeAPI(); api.layout.mockRejectedValue(new RequestError({ code: 'STORAGE_UNAVAILABLE', message: '暂不可用' }, 503))
    api.canvasReceipt.mockRejectedValueOnce(new RequestError({ code: 'MUTATION_NOT_FOUND', message: '未找到回执' }, 404))
    const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api))
    act(() => { result.current.move(serviceId, { x: 500, y: 200 }) }); act(() => { result.current.save() })
    await waitFor(() => { expect(result.current.saveState.kind).toBe('unknown') })
    act(() => { result.current.verify() })
    await waitFor(() => { expect(result.current.saveState).toMatchObject({ kind: 'unknown', message: '未找到回执' }) })
    const command = api.layout.mock.calls[0]?.[1]; if (!command) throw new Error('缺少请求')
    api.canvasReceipt.mockResolvedValue(canvasReceipt({ ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => ({ ...node, position: { x: 500, y: 200 } })) }, 'saveCanvasLayout', command.mutationId))
    act(() => { result.current.verify() }); await waitFor(() => { expect(result.current.dirty).toBe(false) })
  })
  it('确定拒绝与版本冲突保留位置，用户可重放到最新版本或还原', async () => {
    const api = fakeAPI(); api.layout.mockRejectedValue(new RequestError({ code: 'REVISION_CONFLICT', message: '其他窗口已更新' }, 409)); api.graph.mockResolvedValue({ project: { ...INITIAL_PROJECT, revision: '3' }, graph: EMPTY_GRAPH })
    const { result } = renderHook(() => useWorkspace(INITIAL_PROJECT, api))
    act(() => { result.current.move(serviceId, { x: 900, y: 800 }) }); act(() => { result.current.save() })
    await waitFor(() => { expect(result.current.saveState).toMatchObject({ kind: 'rejected', conflict: true }) })
    act(() => { result.current.rebase() }); await waitFor(() => { expect(result.current.snapshot.revision).toBe('3') })
    expect(result.current.draft[0]?.x).toBe(900)
    act(() => { result.current.discard() }); expect(result.current.dirty).toBe(false)
    api.register.mockRejectedValue(new RequestError({ code: 'NOT_GIT_WORKTREE', message: '不是仓库' }, 400))
    act(() => { result.current.register('/allowed/plain', '') }); await waitFor(() => { expect(result.current.saveState).toMatchObject({ kind: 'rejected', conflict: false }) })
  })
  it('旧项目的迟到响应不能写入新工作区，发送中不能改动草稿', async () => {
    const api = fakeAPI(); let finish: ((value: CanvasReceipt) => void) | undefined
    api.layout.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const first = renderHook(() => useWorkspace(INITIAL_PROJECT, api))
    act(() => { first.result.current.move(serviceId, { x: 300, y: 300 }) }); act(() => { first.result.current.save() })
    act(() => { first.result.current.move(serviceId, { x: 999, y: 999 }) }); expect(first.result.current.draft[0]?.x).toBe(300)
    first.unmount()
    const second = renderHook(() => useWorkspace({ ...INITIAL_PROJECT, id: '00000000-0000-4000-8000-000000000099', name: '另一个项目' }, api))
    const command = api.layout.mock.calls[0]?.[1]; if (!command || !finish) throw new Error('未创建请求')
    await act(async () => { finish?.(canvasReceipt(INITIAL_PROJECT, 'saveCanvasLayout', command.mutationId)); await Promise.resolve() })
    expect(second.result.current.snapshot.name).toBe('另一个项目')
  })
})
