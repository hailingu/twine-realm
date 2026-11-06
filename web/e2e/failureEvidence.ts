/** 失败时仅输出测试项目公开投影，不转储环境、私有根、工具原日志或数据库秘密。 */
import { expect, type APIRequestContext, type Page } from '@playwright/test'
import type { BuildSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'

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
      } else process.stderr.write('[BUILD_FAILURE_PROJECTION] HTTP ' + response.status().toString() + '\n')
    })
  }
}
