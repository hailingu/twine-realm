/** 失败时仅输出测试项目公开投影，不转储环境、私有根、工具原日志或数据库秘密。 */
import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { BuildSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'

/** @param value 不可信元数据字段 @returns 可安全读字段的对象，拒绝null和数组。 */
function metadataObject(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === 'object' && !Array.isArray(value) }
/** @param value 不可信字段 @returns 只回显合法内容摘要；其他值只显示类型，绝不转储原字符串。 */
function publicDigest(value: unknown): string { return typeof value === 'string' && /^sha256:[0-9a-f]{64}$/.test(value) ? value : typeof value }

/** @param operationId 测试服务返回的UUID @returns 只记录本次元数据的固定摘要字段与类型，不读取源码、秘密或工具原日志。 */
async function recordBuildMetadata(operationId: string): Promise<void> {
  const root = process.env.TWINE_TEST_BUILD_DIRECTORY
  if (!root) return
  let data: Buffer
  try { data = await readFile(join(root, 'operations', operationId, 'image-metadata.json')) }
  catch (error) {
    if (error instanceof Error && 'code' in error && error.code === 'ENOENT') { process.stderr.write('[BUILD_METADATA_PROJECTION] missing\n'); return }
    throw error
  }
  if (data.length > 65536) { process.stderr.write('[BUILD_METADATA_PROJECTION] oversized\n'); return }
  const value: unknown = JSON.parse(data.toString('utf8'))
  if (!metadataObject(value)) throw new Error('测试构建元数据不是对象')
  const descriptor = value['containerimage.descriptor']
  let descriptorDigest: unknown
  let configAnnotation: unknown
  if (metadataObject(descriptor)) {
    descriptorDigest = descriptor.digest
    if (metadataObject(descriptor.annotations)) configAnnotation = descriptor.annotations['config.digest']
  }
  process.stderr.write('[BUILD_METADATA_PROJECTION] ' + JSON.stringify({ manifest: publicDigest(value['containerimage.digest']), config: publicDigest(value['containerimage.config.digest']), descriptor: publicDigest(descriptorDigest), configAnnotation: publicDigest(configAnnotation), warningsType: typeof value['buildx.build.warnings'] }) + '\n')
}

/** @param original 原失败 @param capture 只读公开诊断 @returns 保留原断言；诊断本身失败也不会覆盖原始原因。 */
async function preserveFailure(original: unknown, capture: () => Promise<void>): Promise<never> {
  try { await capture() }
  catch (evidence) { throw new AggregateError([original, evidence], '原断言及失败诊断均未完成', { cause: evidence }) }
  throw original
}

/** @param page 当前测试页面 @param id 已持久关系身份 @returns 保持原断言与时限，失败时记录可核实的画布投影。 */
export async function expectRestoredEdge(page: Page, id: string): Promise<void> {
  try { await expect(page.locator('.react-flow__edge')).toHaveAttribute('data-id', id) }
  catch (error) {
    return preserveFailure(error, async () => {
      const value = await page.evaluate(() => ({
        caption: document.querySelector('.canvas-caption')?.textContent,
        nodes: [...document.querySelectorAll('.react-flow__node')].map(element => ({ id: element.getAttribute('data-id'), width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height, visibility: getComputedStyle(element).visibility, handles: [...element.querySelectorAll('.react-flow__handle')].map(handle => ({ id: handle.getAttribute('data-handleid'), type: handle.className })) })),
        edges: [...document.querySelectorAll('.react-flow__edge')].map(element => element.getAttribute('data-id'))
      }))
      process.stderr.write('[CANVAS_FAILURE_PROJECTION] ' + JSON.stringify(value) + '\n')
    })
  }
}

/** @param page 测试页面 @param request 真实HTTP测试端口 @param route 已登记服务路由 @returns 保留原成功断言；失败时读安全持久状态区分在途与已失败。 */
export async function expectBuiltCandidate(page: Page, request: APIRequestContext, route: string): Promise<void> {
  try { await expect(page.getByText('当前固定输入已通过构建')).toBeVisible({ timeout: 180000 }) }
  catch (error) {
    return preserveFailure(error, async () => {
      const response = await request.get(route + '/build')
      if (response.ok()) {
        const value = parseContract<BuildSnapshot>('BuildSnapshot', await response.json())
        const operation = value.operation
        const progress = operation ? { id: operation.id, state: operation.state, code: operation.code, message: operation.message, version: operation.version, createdAt: operation.createdAt, updatedAt: operation.updatedAt, steps: operation.steps } : null
        process.stderr.write('[BUILD_FAILURE_PROJECTION] ' + JSON.stringify({ projectId: value.projectId, serviceId: value.serviceId, generation: value.generation, candidateCurrent: value.candidateCurrent, candidateCode: value.candidateCode, operation: progress, policy: value.target?.policy }) + '\n')
        if (operation) await recordBuildMetadata(operation.id)
      } else process.stderr.write('[BUILD_FAILURE_PROJECTION] HTTP ' + response.status().toString() + '\n')
    })
  }
}
