/** 发布schema与跨字段身份都必须通过，格式正确的跨项目回执也不能被接受。 */
import { afterEach, expect, it, vi } from 'vitest'
import { EMPTY_RUNTIME, READY_RUNTIME, RUNTIME_CAPABILITY, RUNTIME_COMMAND, RUNTIME_RECEIPT } from '../testing/runtimeFixtures'
import { INITIAL_PROJECT, MYSQL_NODE } from '../testing/projectFixtures'
import { RUNTIME_API } from './api'

afterEach(() => { vi.unstubAllGlobals() })

it('各真实客户端端点使用同源、原命令与独立契约，每次读取创建新的响应体', async () => {
  let value: object = RUNTIME_CAPABILITY
  const fetcher = vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value))))
  vi.stubGlobal('fetch', fetcher)
  await expect(RUNTIME_API.capability()).resolves.toEqual(RUNTIME_CAPABILITY)
  value = EMPTY_RUNTIME
  await expect(RUNTIME_API.snapshot(INITIAL_PROJECT.id)).resolves.toEqual(EMPTY_RUNTIME)
  value = READY_RUNTIME
  await expect(RUNTIME_API.snapshot(INITIAL_PROJECT.id)).resolves.toEqual(READY_RUNTIME)
  value = RUNTIME_RECEIPT
  await expect(RUNTIME_API.accept(INITIAL_PROJECT.id, MYSQL_NODE.id, RUNTIME_COMMAND)).resolves.toEqual(RUNTIME_RECEIPT)
  expect(fetcher).toHaveBeenLastCalledWith(`/api/v1/projects/${INITIAL_PROJECT.id}/runtime/${MYSQL_NODE.id}/targets`, expect.objectContaining({ method: 'POST', body: JSON.stringify(RUNTIME_COMMAND), headers: { 'Content-Type': 'application/json', 'X-Twine-Request': '1' } }))
  await expect(RUNTIME_API.receipt(RUNTIME_COMMAND.mutationId)).resolves.toEqual(RUNTIME_RECEIPT)
  value = { schemaVersion: 1, events: [], nextSequence: '17', hasMore: false }
  await expect(RUNTIME_API.events(INITIAL_PROJECT.id, '17')).resolves.toEqual(value)
  expect(fetcher).toHaveBeenLastCalledWith(`/api/v1/projects/${INITIAL_PROJECT.id}/runtime/events?after=17`, expect.anything())
})

it('跨项目、矛盾代次、无操作目标及未知目录响应不能推进运行投影', async () => {
  for (const value of [
    { ...EMPTY_RUNTIME, projectId: MYSQL_NODE.id },
    { ...EMPTY_RUNTIME, generation: '1' },
    { ...READY_RUNTIME, generation: '2' },
    { ...READY_RUNTIME, operation: null },
    { ...READY_RUNTIME, target: { ...RUNTIME_RECEIPT.target, projectId: MYSQL_NODE.id } },
    { ...READY_RUNTIME, target: { ...RUNTIME_RECEIPT.target, resource: { ...RUNTIME_RECEIPT.target.resource, definition: { ...MYSQL_NODE.definition, version: '2.0.0' } } } },
    { ...READY_RUNTIME, observation: { ...READY_RUNTIME.observation, ready: true, serverUuid: '', volumeFound: false } }
  ]) {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
    await expect(RUNTIME_API.snapshot(INITIAL_PROJECT.id)).rejects.toMatchObject({ status: 0 })
  }
})

it('受理回执必须匹配原键、组件、目标和下一代次，查询回执也必须匹配原键', async () => {
  for (const value of [
    { ...RUNTIME_RECEIPT, mutationId: MYSQL_NODE.id },
    { ...RUNTIME_RECEIPT, target: { ...RUNTIME_RECEIPT.target, desired: 'stopped' } },
    { ...RUNTIME_RECEIPT, target: { ...RUNTIME_RECEIPT.target, generation: '2' } },
    { ...RUNTIME_RECEIPT, target: { ...RUNTIME_RECEIPT.target, resource: { ...RUNTIME_RECEIPT.target.resource, componentId: INITIAL_PROJECT.id } } }
  ]) {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
    await expect(RUNTIME_API.accept(INITIAL_PROJECT.id, MYSQL_NODE.id, RUNTIME_COMMAND)).rejects.toMatchObject({ status: 0 })
  }
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ ...RUNTIME_RECEIPT, mutationId: MYSQL_NODE.id })))))
  await expect(RUNTIME_API.receipt(RUNTIME_COMMAND.mutationId)).rejects.toMatchObject({ status: 0 })
})
