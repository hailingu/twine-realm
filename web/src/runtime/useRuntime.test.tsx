/** Hook时序测试验证双击、原键恢复和卸载所有权；真实后台延续另由浏览器验收。 */
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { RuntimeReceipt } from '../contract/generated'
import { INITIAL_PROJECT, MYSQL_NODE } from '../testing/projectFixtures'
import { READY_RUNTIME, RUNTIME_RECEIPT, fakeRuntimeAPI } from '../testing/runtimeFixtures'
import { RequestError } from '../project/api'
import { useRuntime } from './useRuntime'

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

it('双击只受理一次，响应丢失后原键重试，已受理仍等待新投影', async () => {
  const api = fakeRuntimeAPI()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  const { result } = renderHook(() => useRuntime(api, INITIAL_PROJECT.id))
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running'); result.current.run(MYSQL_NODE.id, '1', 'running') })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  expect(api.accept).toHaveBeenCalledTimes(1)
  const original = api.accept.mock.calls[0]?.[2]
  expect(original).toBeDefined()
  act(() => { result.current.retry() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('accepted') })
  expect(api.accept.mock.calls[1]?.[2]).toEqual(original)
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'stopped') })
  expect(api.accept).toHaveBeenCalledTimes(2)
})

it('核实只查询原回执，错误归属保持未知，成功后轮询确认再允许新目标', async () => {
  const api = fakeRuntimeAPI()
  api.accept.mockRejectedValueOnce(new Error('断开'))
  const { result } = renderHook(() => useRuntime(api, INITIAL_PROJECT.id))
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running') })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  const command = api.accept.mock.calls[0]?.[2]
  if (!command) throw new Error('缺少原意图')
  api.receipt.mockResolvedValueOnce({ ...RUNTIME_RECEIPT, mutationId: command.mutationId, target: { ...RUNTIME_RECEIPT.target, projectId: MYSQL_NODE.id } })
  act(() => { result.current.verify() })
  await waitFor(() => { expect(result.current.state.submission).toMatchObject({ kind: 'unknown', verifying: false }) })
  api.receipt.mockResolvedValueOnce({ ...RUNTIME_RECEIPT, mutationId: command.mutationId })
  act(() => { result.current.verify() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('accepted') })
  expect(api.accept).toHaveBeenCalledTimes(1)
  expect(api.receipt).toHaveBeenLastCalledWith(command.mutationId, expect.any(AbortSignal))
  api.snapshot.mockResolvedValue(READY_RUNTIME)
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('idle') }, { timeout: 3500 })
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'stopped') })
  await waitFor(() => { expect(api.accept).toHaveBeenCalledTimes(2) })
  expect(api.accept.mock.calls[1]?.[2]).toMatchObject({ desired: 'stopped', baseGeneration: '1', retryOf: READY_RUNTIME.operation?.id })
})

it('已知拒绝解除原请求保护，卸载仅中止客户端，不补发停止或取消后台目标', async () => {
  const api = fakeRuntimeAPI()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'RUNTIME_GENERATION_CONFLICT', message: '目标已改变' }, 409))
  let resolve: ((value: RuntimeReceipt) => void) | undefined
  const { result, unmount } = renderHook(() => useRuntime(api, INITIAL_PROJECT.id))
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running') })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('rejected') })
  api.accept.mockImplementationOnce(() => new Promise<RuntimeReceipt>(accept => { resolve = accept }))
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running') })
  const signal = api.accept.mock.calls[1]?.[3]
  unmount()
  expect(signal?.aborted).toBe(true)
  await act(async () => { resolve?.(RUNTIME_RECEIPT); await Promise.resolve() })
  expect(api.accept).toHaveBeenCalledTimes(2)
})

it('能力未读取或接口未配置时没有隐式运行操作，轮询失败保留诊断', async () => {
  const api = fakeRuntimeAPI()
  api.snapshot.mockRejectedValue(new Error('数据库失联'))
  const { result } = renderHook(() => useRuntime(api, INITIAL_PROJECT.id))
  act(() => { result.current.run(MYSQL_NODE.id, '1', 'running'); result.current.retry(); result.current.verify() })
  await waitFor(() => { expect(result.current.state.readError).not.toBeNull() })
  expect(api.accept).not.toHaveBeenCalled()
  const disabled = renderHook(() => useRuntime(undefined, INITIAL_PROJECT.id))
  act(() => { disabled.result.current.run(MYSQL_NODE.id, '1', 'running') })
  expect(disabled.result.current.state.snapshot).toBeNull()
})
