/** 验证外部响应先通过发布契约，网络失败不被解释成成功。 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { parseContract, PROJECT_API, RequestError } from './api'
import { EMPTY_CANVAS, INITIAL_CATALOG, INITIAL_PROJECT, INITIAL_SOURCE, MYSQL_NODE, canvasReceipt, receipt, sourceReceipt } from '../testing/projectFixtures'

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
  it('源码增量契约与原回执分离，固定命令携带原请求身份', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(INITIAL_SOURCE)))
    vi.stubGlobal('fetch', fetcher)
    await expect(PROJECT_API.source(INITIAL_PROJECT.id, INITIAL_SOURCE.version.serviceId)).resolves.toEqual(INITIAL_SOURCE)
    const command = { mutationId: crypto.randomUUID(), baseRevision: '1', commit: 'a'.repeat(40) }
    const accepted = sourceReceipt(command.mutationId)
    fetcher.mockResolvedValue(new Response(JSON.stringify(accepted)))
    await expect(PROJECT_API.pin(INITIAL_PROJECT.id, INITIAL_SOURCE.version.serviceId, command)).resolves.toEqual(accepted)
    expect(fetcher).toHaveBeenLastCalledWith(`/api/v1/projects/${INITIAL_PROJECT.id}/services/${INITIAL_SOURCE.version.serviceId}/source`, expect.objectContaining({ method: 'PUT', body: JSON.stringify(command) }))
    fetcher.mockResolvedValue(new Response(JSON.stringify(accepted)))
    await expect(PROJECT_API.sourceReceipt(command.mutationId)).resolves.toEqual(accepted)
    expect(fetcher).toHaveBeenLastCalledWith(`/api/v1/source-mutations/${command.mutationId}`, expect.anything())
    expect(() => parseContract('Receipt', accepted)).toThrow(RequestError)
    expect(() => parseContract('SourceObservation', { ...INITIAL_SOURCE, git: { ...INITIAL_SOURCE.git, dirty: 'false' } })).toThrow(RequestError)
  })
  it('目录、完整画布及专用回执使用真实客户端边界，版本不一致时阻止编辑', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ project: INITIAL_PROJECT, canvas: EMPTY_CANVAS })))
    vi.stubGlobal('fetch', fetcher)
    await expect(PROJECT_API.canvas(INITIAL_PROJECT.id)).resolves.toMatchObject({ project: INITIAL_PROJECT, canvas: EMPTY_CANVAS })
    fetcher.mockResolvedValue(new Response(JSON.stringify(INITIAL_CATALOG)))
    await expect(PROJECT_API.catalog()).resolves.toEqual(INITIAL_CATALOG)
    fetcher.mockResolvedValue(new Response(JSON.stringify({ ...INITIAL_CATALOG, digest: 'sha256:' + '0'.repeat(64) })))
    await expect(PROJECT_API.catalog()).rejects.toThrow('版本不一致')
    fetcher.mockResolvedValue(new Response(JSON.stringify({ ...INITIAL_CATALOG, definitions: [] })))
    await expect(PROJECT_API.catalog()).rejects.toThrow('版本不一致')
    const command = { mutationId: crypto.randomUUID(), baseRevision: '1', definition: MYSQL_NODE.definition, displayName: '数据库', configuration: {}, position: MYSQL_NODE.position }
    const added = canvasReceipt(INITIAL_PROJECT, 'addMiddleware', command.mutationId, { schemaVersion: 1, middlewares: [MYSQL_NODE] })
    fetcher.mockResolvedValue(new Response(JSON.stringify(added)))
    await expect(PROJECT_API.addMiddleware(INITIAL_PROJECT.id, command)).resolves.toEqual(added)
    fetcher.mockResolvedValue(new Response(JSON.stringify({ ...added, kind: 'updateMiddleware' })))
    await PROJECT_API.updateMiddleware(INITIAL_PROJECT.id, MYSQL_NODE.id, command)
    fetcher.mockResolvedValue(new Response(JSON.stringify(added)))
    await expect(PROJECT_API.canvasReceipt(command.mutationId)).resolves.toEqual(added)
    expect(() => parseContract('Receipt', added)).toThrow(RequestError)
  })
  it('普通数组类型保留同一运行时数量门槛，超限载荷仍拒绝', () => {
    const definitions = Array.from({ length: 21 }, () => INITIAL_CATALOG.definitions[0])
    expect(() => parseContract('DefinitionCatalog', { ...INITIAL_CATALOG, definitions })).toThrow(RequestError)
    const middlewares = Array.from({ length: 501 }, () => MYSQL_NODE)
    expect(() => parseContract('CanvasSnapshot', { project: INITIAL_PROJECT, canvas: { schemaVersion: 1, middlewares } })).toThrow(RequestError)
  })
})
