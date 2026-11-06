/** 构建会话验证同键未知恢复、取消与卸载，不以模拟端口证明后端镜像成功。 */
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { BUILD_OPERATION, BUILD_RECEIPT, EMPTY_BUILD, RUNNING_BUILD, fakeBuildAPI } from '../testing/buildFixtures'
import { RequestError } from '../project/api'
import { useBuild } from './useBuild'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('双击只发送一次，丢失响应按原键核实和重试，不换构建意图', async () => {
  const api = fakeBuildAPI()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '响应丢失' }, 0))
  const { result } = renderHook(() => useBuild(api, EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, '1'))
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.build(); result.current.build() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  expect(api.accept).toHaveBeenCalledTimes(1)
  const command = api.accept.mock.calls[0]?.[2]
  if (!command) throw new Error('缺少原请求')
  api.receipt.mockRejectedValueOnce(new RequestError({ code: 'MUTATION_NOT_FOUND', message: '原键尚未确认' }, 404))
  act(() => { result.current.verify() })
  await waitFor(() => { expect(api.receipt).toHaveBeenCalledWith(command.mutationId, expect.any(AbortSignal)); expect(result.current.state.submission.kind).toBe('unknown') })
  act(() => { result.current.retry() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('idle') })
  expect(api.accept.mock.calls[1]?.[2]).toEqual(command)
})

it('未知取消保留原操作与操作版本，原键回执不能关联其他服务', async () => {
  const api = fakeBuildAPI(); api.snapshot.mockResolvedValue(RUNNING_BUILD)
  api.cancel.mockRejectedValueOnce(new RequestError({ code: 'TRANSPORT_FAILED', message: '取消响应丢失' }, 0))
  const { result } = renderHook(() => useBuild(api, EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, '1'))
  await waitFor(() => { expect(result.current.state.snapshot?.operation?.id).toBe(BUILD_OPERATION.id) })
  act(() => { result.current.cancel() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  const command = api.cancel.mock.calls[0]?.[3]
  if (!command) throw new Error('缺少原取消请求')
  api.receipt.mockResolvedValueOnce({ ...BUILD_RECEIPT, mutationId: command.mutationId, kind: 'cancelBuild', version: '3', target: { ...BUILD_RECEIPT.target, serviceId: BUILD_RECEIPT.operationId } })
  act(() => { result.current.verify() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('unknown') })
  act(() => { result.current.retry() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('idle') })
  expect(api.cancel.mock.calls[1]?.[2]).toBe(BUILD_OPERATION.id)
  expect(api.cancel.mock.calls[1]?.[3]).toEqual(command)
})

it('明确拒绝解除原键保护，读失败或源码版本未确认时不发送新构建', async () => {
  const api = fakeBuildAPI()
  api.accept.mockRejectedValueOnce(new RequestError({ code: 'REVISION_CONFLICT', message: '画布已改变' }, 409))
  const { result, rerender } = renderHook(({ revision }) => useBuild(api, EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, revision), { initialProps: { revision: '1' } })
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.build() })
  await waitFor(() => { expect(result.current.state.submission.kind).toBe('rejected') })
  rerender({ revision: '2' })
  act(() => { result.current.build() })
  expect(api.accept).toHaveBeenCalledTimes(1)
  api.snapshot.mockRejectedValueOnce(new Error('读取失败')); rerender({ revision: '3' })
  // 读取互斥时由下一次有界轮询消费，不能用重复并发读取满足断言。
  await waitFor(() => { expect(result.current.state.readError).not.toBeNull() }, { timeout: 3500 })
  act(() => { result.current.build() }); expect(api.accept).toHaveBeenCalledTimes(1)
})

it('卸载终止读取和计时器，原构建由服务器继续拥有且不隐含取消', async () => {
  const api = fakeBuildAPI()
  let finish: ((value: typeof BUILD_RECEIPT) => void) | undefined
  api.accept.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  const { result, unmount } = renderHook(() => useBuild(api, EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, '1'))
  await waitFor(() => { expect(result.current.state.snapshot).not.toBeNull() })
  act(() => { result.current.build() }); const command = api.accept.mock.calls[0]?.[2]
  if (!command) throw new Error('缺少原请求')
  const signal = api.accept.mock.calls[0]?.[3]
  unmount(); expect(signal?.aborted).toBe(true)
  await act(async () => { finish?.({ ...BUILD_RECEIPT, mutationId: command.mutationId }); await Promise.resolve() })
  expect(api.cancel).not.toHaveBeenCalled()
})
