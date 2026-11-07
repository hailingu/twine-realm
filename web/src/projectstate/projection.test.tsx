/** 统一投影与命令寿命分离，SSE断开不解除或取消原请求。 */
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { ProjectionContext, type StateProjection } from './Projection'
import { useProjectState } from './useProjectState'
import { StateReadError } from './api'
import { controlledStateAPI, stateSnapshot, stateStream } from '../testing/stateFixtures'
import { INITIAL_PROJECT, MYSQL_NODE } from '../testing/projectFixtures'
import { READY_RUNTIME, RUNTIME_RECEIPT, fakeRuntimeAPI } from '../testing/runtimeFixtures'
import { READY_BUILD, fakeBuildAPI } from '../testing/buildFixtures'
import { READY_DEPLOYMENT, fakeDeploymentAPI } from '../testing/deploymentFixtures'
import { useRuntime } from '../runtime/useRuntime'
import { useBuild } from '../servicebuild/useBuild'
import { useDeploymentRead } from '../servicedeploy/useDeploymentRead'
import { RequestError } from '../project/api'

afterEach(() => { cleanup(); vi.useRealTimers() })

it('所选服务以一致投影显示元数据，独立候选核实不能覆盖新代次', async () => {
  const runtimeApi = fakeRuntimeAPI(); const buildApi = fakeBuildAPI(); const deploymentApi = fakeDeploymentAPI()
  buildApi.snapshot.mockResolvedValue(READY_BUILD)
  let projection: StateProjection = { managed: true, error: '', now: Date.now(), snapshot: { ...stateSnapshot(), runtime: READY_RUNTIME, builds: [{ ...READY_BUILD, candidateCurrent: false }], deployments: [READY_DEPLOYMENT] } }
  const Wrapper = ({ children }: { children: ReactNode }) => <ProjectionContext.Provider value={projection}>{children}</ProjectionContext.Provider>
  const { result, rerender } = renderHook(() => ({ runtime: useRuntime(runtimeApi, INITIAL_PROJECT.id), build: useBuild(buildApi, INITIAL_PROJECT.id, READY_BUILD.serviceId, '1'), deployment: useDeploymentRead(deploymentApi, buildApi, INITIAL_PROJECT.id, READY_BUILD.serviceId) }), { wrapper: Wrapper })
  await waitFor(() => { expect(result.current.build.state.snapshot?.candidateCurrent).toBe(true); expect(result.current.deployment.build?.candidateCurrent).toBe(true) })
  expect(result.current.runtime.state.snapshot?.generation).toBe('1'); expect(result.current.deployment.snapshot?.sequence).toBe('20')
  expect(runtimeApi.snapshot).not.toHaveBeenCalled(); expect(deploymentApi.snapshot).not.toHaveBeenCalled()
  projection = { ...projection, error: '状态断开', snapshot: { ...stateSnapshot('2'), builds: [{ ...READY_BUILD, generation: '2', candidateCurrent: false }], deployments: [READY_DEPLOYMENT] } }
  rerender()
  await waitFor(() => { expect(result.current.build.state.snapshot?.candidateCurrent).toBe(false) })
  expect(result.current.build.state.readError).toBe('状态断开'); expect(result.current.deployment.readError).toBe('状态断开')
})

it('断流不会abort提交controller，运行投影到达也不能替代原键HTTP核实', async () => {
  const api = fakeRuntimeAPI(); let rejectRequest: ((reason: Error) => void) | undefined
  api.accept.mockImplementation(() => new Promise((_resolve, reject) => { rejectRequest = reject }))
  let projection: StateProjection = { managed: true, error: '', now: Date.now(), snapshot: stateSnapshot() }
  const Wrapper = ({ children }: { children: ReactNode }) => <ProjectionContext.Provider value={projection}>{children}</ProjectionContext.Provider>
  const { result, rerender } = renderHook(() => useRuntime(api, INITIAL_PROJECT.id), { wrapper: Wrapper })
  await waitFor(() => { expect(result.current.state.capability?.enabled).toBe(true) })
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running') })
  const original = api.accept.mock.calls[0]; if (!original || !rejectRequest) throw new Error('没有原运行请求')
  projection = { ...projection, error: '网络断开' }; rerender()
  expect(original[3]?.aborted).toBe(false)
  act(() => { rejectRequest?.(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0)) })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  projection = { ...projection, error: '', snapshot: { ...stateSnapshot('2'), runtime: READY_RUNTIME } }; rerender()
  await waitFor(() => { expect(result.current.state.snapshot?.generation).toBe('1') })
  expect(result.current.state.submission.kind).toBe('unknown')
  api.receipt.mockResolvedValue({ ...RUNTIME_RECEIPT, mutationId: original[2].mutationId })
  act(() => { result.current.verify() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('idle') })
  expect(api.receipt).toHaveBeenCalledWith(original[2].mutationId, original[3]); expect(api.accept).toHaveBeenCalledTimes(1)
})

it('失败后时钟继续推进，显式重连创建新拥有者，卸载释放读取和时钟', async () => {
  vi.useFakeTimers(); const { api, streams } = controlledStateAPI()
  const { result, unmount } = renderHook(() => useProjectState(api, INITIAL_PROJECT.id))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  const before = result.current.now
  await act(async () => { stateStream(streams).fail(new StateReadError('请核实', false, null, true)); await vi.advanceTimersByTimeAsync(0) })
  expect(result.current.phase).toBe('failed'); expect(result.current.snapshot?.cursor).toBe('1')
  await act(async () => { await vi.advanceTimersByTimeAsync(21000) })
  expect(result.current.now - before).toBe(21000)
  act(() => { result.current.reconnect() }); await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(stateStream(streams, 1).cursor).toBe('1')
  unmount(); await vi.advanceTimersByTimeAsync(0)
  expect(streams.every(value => value.signal.aborted)).toBe(true); expect(vi.getTimerCount()).toBe(0)
})
