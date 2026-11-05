/** 验证外部响应先通过发布契约，网络失败不被解释成成功。 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { parseContract, PROJECT_API, RequestError } from './api'
import { INITIAL_PROJECT, receipt } from '../testing/projectFixtures'

afterEach(() => { vi.unstubAllGlobals() })
describe('API 信任边界', () => {
  it('校验版本、坐标、源提交和额外字段', () => {
    expect(parseContract('Project', INITIAL_PROJECT)).toEqual(INITIAL_PROJECT)
    for (const input of [{ ...INITIAL_PROJECT, schemaVersion: 2 }, { ...INITIAL_PROJECT, nodes: [{}] }, { ...INITIAL_PROJECT, secret: '不可进入模型' }]) expect(() => parseContract('Project', input)).toThrow(RequestError)
    expect(() => parseContract('not-registered', {})).toThrow('未注册')
  })
  it('真实客户端验证各端点并为写请求附加同源 JSON 标记', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(INITIAL_PROJECT)))
    vi.stubGlobal('fetch', fetcher)
    await expect(PROJECT_API.get(INITIAL_PROJECT.id)).resolves.toEqual(INITIAL_PROJECT)
    fetcher.mockResolvedValue(new Response(JSON.stringify({ projects: [] })))
    await expect(PROJECT_API.list()).resolves.toEqual({ projects: [] })
    const command = { mutationId: crypto.randomUUID(), name: '本地项目' }
    fetcher.mockResolvedValue(new Response(JSON.stringify(receipt(INITIAL_PROJECT, 'createProject', command.mutationId))))
    await PROJECT_API.create(command)
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects', expect.objectContaining({ method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Twine-Request': '1' }, body: JSON.stringify(command) }))
    fetcher.mockResolvedValue(new Response(JSON.stringify({ error: { code: 'REVISION_CONFLICT', message: '版本冲突', currentRevision: '2' } }), { status: 409 }))
    await expect(PROJECT_API.layout(INITIAL_PROJECT.id, { mutationId: crypto.randomUUID(), baseRevision: '1', positions: [] })).rejects.toMatchObject({ status: 409 })
    fetcher.mockRejectedValue(new Error('断开'))
    await expect(PROJECT_API.register(INITIAL_PROJECT.id, { mutationId: crypto.randomUUID(), baseRevision: '1', repositoryPath: '/allowed/repo', position: { x: 0, y: 0 } })).rejects.toMatchObject({ status: 0 })
    fetcher.mockResolvedValue(new Response('{}'))
    await expect(PROJECT_API.receipt(command.mutationId)).rejects.toMatchObject({ status: 0 })
  })
})
