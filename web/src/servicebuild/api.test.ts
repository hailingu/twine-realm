/** 发布schema与跨字段归属同时通过才允许构建状态进入界面。 */
import { afterEach, expect, it, vi } from 'vitest'
import { BUILD_CANDIDATE, BUILD_CAPABILITY, BUILD_COMMAND, BUILD_RECEIPT, EMPTY_BUILD, READY_BUILD, RUNNING_BUILD } from '../testing/buildFixtures'
import { BUILD_API } from './api'

afterEach(() => { vi.unstubAllGlobals() })

it('实际配置单证据和缺配置的描述符均可恢复，缺失字段保持省略', async () => {
  const image = BUILD_CANDIDATE.image
  const variants = [
    { id: image.id, idKind: 'config', configDigest: image.configDigest, exportDigest: image.configDigest, tag: image.tag, binaryDigest: image.binaryDigest },
    ...['application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json', 'application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json'].map(manifestMediaType => ({ id: image.manifestDigest, idKind: manifestMediaType.includes('index') || manifestMediaType.includes('list') ? 'index' : 'manifest', manifestDigest: image.manifestDigest, manifestMediaType, exportDigest: image.manifestDigest, tag: image.tag, binaryDigest: image.binaryDigest }))
  ]
  let value: object = READY_BUILD
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
  for (const proof of variants) {
    const candidate = { ...BUILD_CANDIDATE, image: proof }
    value = { ...READY_BUILD, candidate, operation: { ...READY_BUILD.operation, candidate } }
    const result = await BUILD_API.snapshot(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId)
    expect(result.candidate?.image).toEqual(proof)
  }
})

it('拒绝内容种类、导出摘要和描述符的矛盾，操作内候选同样校验', async () => {
  const image = BUILD_CANDIDATE.image
  const invalid = [
    { ...image, idKind: 'index' }, { ...image, exportDigest: image.id },
    { ...image, manifestMediaType: 'unknown' }, { ...image, manifestDigest: image.configDigest },
    { ...image, configDigest: undefined }, { ...image, idKind: 'config', manifestDigest: undefined },
    { ...image, idKind: 'manifest', id: image.manifestDigest, manifestMediaType: undefined }
  ]
  let value: object = READY_BUILD
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
  for (const proof of invalid) {
    const candidate = { ...BUILD_CANDIDATE, image: proof }
    value = { ...READY_BUILD, candidate, operation: { ...READY_BUILD.operation, candidate } }
    await expect(BUILD_API.snapshot(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId)).rejects.toMatchObject({ status: 0 })
    value = { ...READY_BUILD, operation: { ...READY_BUILD.operation, candidate } }
    await expect(BUILD_API.snapshot(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId)).rejects.toMatchObject({ status: 0 })
  }
})

it('containerd实际本地ID可为已检查manifest，仍保留独立config摘要并拒绝任意第三摘要', async () => {
  const candidate = { ...BUILD_CANDIDATE, image: { ...BUILD_CANDIDATE.image, idKind: 'manifest', id: BUILD_CANDIDATE.image.manifestDigest } }
  const value = { ...READY_BUILD, candidate, operation: { ...RUNNING_BUILD.operation, state: 'succeeded', candidate } }
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
  await expect(BUILD_API.snapshot(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId)).resolves.toMatchObject({ candidate: { image: { id: candidate.image.manifestDigest, configDigest: candidate.image.configDigest } } })
})

it('真实端点保持同源v3和原请求，成功镜像含独立manifest与config摘要', async () => {
  let value: object = BUILD_CAPABILITY
  const fetcher = vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value))))
  vi.stubGlobal('fetch', fetcher)
  await expect(BUILD_API.capability()).resolves.toEqual(BUILD_CAPABILITY)
  for (const snapshot of [EMPTY_BUILD, RUNNING_BUILD, READY_BUILD]) { value = snapshot; await expect(BUILD_API.snapshot(snapshot.projectId, snapshot.serviceId)).resolves.toEqual(snapshot) }
  value = BUILD_RECEIPT
  await expect(BUILD_API.accept(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, BUILD_COMMAND)).resolves.toEqual(BUILD_RECEIPT)
  expect(fetcher).toHaveBeenLastCalledWith(`/api/v3/projects/${EMPTY_BUILD.projectId}/services/${EMPTY_BUILD.serviceId}/builds`, expect.objectContaining({ method: 'POST', body: JSON.stringify(BUILD_COMMAND) }))
  await expect(BUILD_API.receipt(BUILD_COMMAND.mutationId)).resolves.toEqual(BUILD_RECEIPT)
  value = { ...BUILD_RECEIPT, kind: 'cancelBuild', version: '3' }
  await expect(BUILD_API.cancel(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, BUILD_RECEIPT.operationId, { mutationId: BUILD_COMMAND.mutationId, baseVersion: '2' })).resolves.toMatchObject({ kind: 'cancelBuild' })
})

it('拒绝结构有效的跨项目、旧代次、伪候选及错误原键，不复用Response', async () => {
  let value: object = EMPTY_BUILD
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(value)))))
  const invalid = [{ ...EMPTY_BUILD, projectId: BUILD_COMMAND.mutationId }, { ...EMPTY_BUILD, generation: '1' }, { ...RUNNING_BUILD, operation: { ...RUNNING_BUILD.operation, targetId: BUILD_COMMAND.mutationId } }, { ...READY_BUILD, candidateCode: 'INPUT_CHANGED' }, { ...READY_BUILD, currentSource: { ...READY_BUILD.currentSource, pinnedCommit: 'b'.repeat(40) } }, { ...READY_BUILD, candidate: { ...READY_BUILD.candidate, image: { ...READY_BUILD.candidate?.image, id: 'sha256:' + 'c'.repeat(64) } } }]
  for (const item of invalid) { value = item; await expect(BUILD_API.snapshot(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId)).rejects.toMatchObject({ status: 0 }) }
  for (const item of [{ ...BUILD_RECEIPT, mutationId: BUILD_RECEIPT.operationId }, { ...BUILD_RECEIPT, target: { ...BUILD_RECEIPT.target, generation: '2' } }, { ...BUILD_RECEIPT, kind: 'cancelBuild' }]) { value = item; await expect(BUILD_API.accept(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, BUILD_COMMAND)).rejects.toMatchObject({ status: 0 }) }
  value = { ...BUILD_RECEIPT, operationId: BUILD_COMMAND.mutationId, kind: 'cancelBuild', version: '3' }
  await expect(BUILD_API.cancel(EMPTY_BUILD.projectId, EMPTY_BUILD.serviceId, BUILD_RECEIPT.operationId, { mutationId: BUILD_COMMAND.mutationId, baseVersion: '2' })).rejects.toMatchObject({ status: 0 })
  value = { ...BUILD_RECEIPT, mutationId: BUILD_RECEIPT.operationId }
  await expect(BUILD_API.receipt(BUILD_COMMAND.mutationId)).rejects.toMatchObject({ status: 0 })
})
