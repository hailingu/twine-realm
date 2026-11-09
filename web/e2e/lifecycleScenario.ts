/** 复用已真实部署的浏览器夹具核实父停止、会话恢复和原资源恢复，不另建Docker资源。 */
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import type { ErrorResponse, ProjectRunCommand, ProjectRunReceipt, ProjectRunSnapshot, ProjectRunStateSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'
import { interceptionCompletion } from './interceptionCompletion'

/** @param request HTTP边界 @param route 父状态路由 @returns 真实持久父快照。 */
async function snapshot(request: APIRequestContext, route: string): Promise<ProjectRunSnapshot> {
  const response = await request.get(route)
  expect(response.status()).toBe(200)
  const value = parseContract<ProjectRunSnapshot>('ProjectRunSnapshot', await response.json())
  if (value.operation && ['failed', 'needs_attention'].includes(value.operation.state)) throw new Error(`父操作未完成：${value.operation.code} ${value.operation.message}`)
  return value
}

/** @param page 真实工作台 @param request 真实HTTP @param url 平台地址 @param id 已部署项目 @param entry 原业务入口 @returns 停止和恢复同一物理资源的浏览器证据。 */
export async function verifyLifecycle(page: Page, request: APIRequestContext, url: string, id: string, entry: string): Promise<void> {
  const route = `${url}/api/v6/projects/${id}/lifecycle`
  const commands: ProjectRunCommand[] = []
  let accepted: ProjectRunReceipt | undefined
  const stopInterception = interceptionCompletion()
  await page.route(route, async intercepted => {
    if (intercepted.request().method() !== 'POST') { await intercepted.continue(); return }
    commands.push(parseContract<ProjectRunCommand>('ProjectRunCommand', intercepted.request().postDataJSON()))
    if (commands.length !== 1) { await intercepted.continue(); return }
    await stopInterception.run(async () => {
      const started = Date.now()
      let status: number | null = null
      let fetchElapsedMs: number | null = null
      try {
        const response = await intercepted.fetch()
        fetchElapsedMs = Date.now() - started
        status = response.status()
        if (status !== 202) {
          const failure = parseContract<ErrorResponse>('ErrorResponse', await response.json())
          await intercepted.fulfill({ response })
          throw new Error(`真实父停止受理失败：${status} ${failure.error.code} ${failure.error.message}`)
        }
        accepted = parseContract<ProjectRunReceipt>('ProjectRunReceipt', await response.json())
        await intercepted.abort('failed')
      } finally {
        await test.info().attach('父停止受理时间与状态', { body: JSON.stringify({ elapsedMs: Date.now() - started, fetchElapsedMs, status }), contentType: 'application/json' })
      }
    })
  })
  // 控制面刚重启，等真实组件认证与观察完成，不以历史成功操作替代近期事实。
  await expect.poll(async () => {
    const response = await request.get(`${url}/api/v6/projects/${id}/state`)
    expect(response.status()).toBe(200)
    const value = parseContract<ProjectRunStateSnapshot>('ProjectRunStateSnapshot', await response.json()).state
    return value.runtime.operation?.state === 'succeeded' && value.runtime.freshness === 'fresh' && value.runtime.observation?.ready === true && value.deployments.filter(item => item.target !== null).length === 1 && value.deployments.filter(item => item.target !== null).every(item => item.operation?.state === 'succeeded' && item.freshness === 'fresh' && item.observation?.ready === true)
  }, { timeout: 30000 }).toBe(true)
  await expect(page.getByRole('button', { name: '停止项目并保留数据' })).toBeEnabled()
  await page.getByRole('button', { name: '停止项目并保留数据' }).click()
  await test.step('等待真实202受理并完成模拟丢响应', () => stopInterception.wait())
  await test.step('显示原项目请求恢复入口', async () => {
    await expect(page.getByRole('button', { name: '核实原项目请求' })).toBeVisible()
  })
  await expect.poll(async () => (await snapshot(request, route)).operation?.state, { timeout: 120000 }).toBe('succeeded')
  const stopped = await snapshot(request, route)
  expect(stopped.facts?.applicationStopped).toBe(true); expect(stopped.facts?.databaseStopped).toBe(true)
  await page.reload(); await page.getByRole('button', { name: /独立服务部署验收/ }).click()
  await expect(page.getByRole('button', { name: '核实原项目请求' })).toBeVisible()
  expect(commands).toHaveLength(1)
  if (!accepted) throw new Error('缺少原停止受理回执')
  const receiptResponse = page.waitForResponse(response => response.url() === `${route}/receipts/${accepted?.mutationId}` && response.request().method() === 'GET')
  await page.getByRole('button', { name: '核实原项目请求' }).click()
  const response = await receiptResponse
  expect(response.status()).toBe(200)
  const recovered = parseContract<ProjectRunReceipt>('ProjectRunReceipt', await response.json())
  expect(recovered.mutationId).toBe(accepted.mutationId)
  expect(recovered.operationId).toBe(accepted.operationId)
  // 按钮在sending阶段已经隐藏，必须等页面处理明确回执后才检查恢复键清除。
  await expect(page.getByText('原请求已受理；实际停止或运行结果以后台核实事实为准。', { exact: true })).toBeVisible()
  expect(stopped.operation?.id).toBe(accepted.operationId)
  expect(await page.evaluate(project => sessionStorage.getItem(`twine-project-lifecycle:${project}`), id)).toBeNull()
  await expect(page.getByRole('button', { name: '恢复原项目' })).toBeEnabled()
  await page.getByRole('button', { name: '恢复原项目' }).click()
  await expect.poll(async () => {
    const value = await snapshot(request, route)
    return value.target?.desired === 'running' && value.operation?.state === 'succeeded'
  }, { timeout: 150000 }).toBe(true)
  const restored = await snapshot(request, route)
  expect(restored.target?.frozen).toEqual(stopped.target?.frozen)
  expect(restored.facts?.runtime?.serverUuid).toBe(stopped.target?.frozen.serverUuid)
  expect(restored.facts?.deployment?.containerId).toBe(stopped.target?.frozen.applicationContainerId)
  expect((await request.get(entry + '/readyz')).status()).toBe(200)
  expect(commands).toHaveLength(2)
  expect(commands[0]?.desired).toBe('stopped'); expect(commands[1]?.desired).toBe('running')
  await page.unroute(route)
}
