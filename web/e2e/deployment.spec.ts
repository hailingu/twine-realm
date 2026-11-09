/** 真实浏览器从固定源码构建到审阅部署、原键恢复及独立业务入口，不使用模拟响应证明成功。 */
import { expect, type APIRequestContext } from '@playwright/test'
import { execFile } from 'node:child_process'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { promisify } from 'node:util'
import type { CanvasReceipt, DeployCommand, DeploySnapshot, DefinitionCatalog, GraphReceipt, GraphSnapshot } from '../src/contract/generated'
import { SERVICE_CONTRACT_EXAMPLE } from '../src/contract/graph.generated'
import { parseContract } from '../src/project/api'
import { test, type Platform } from './platformFixture'
import { expectBuiltCandidate } from './failureEvidence'
import { verifyLifecycle } from './lifecycleScenario'

const execute = promisify(execFile)

/** @param platform 原测试平台 @returns 已提交的独立最小服务，Todo CRUD另由真实Go集成验收。 */
async function repository(platform: Platform): Promise<string> {
  const cli = process.env.TWINE_TEST_PROFILE_BINARY
  if (!cli) throw new Error('真实浏览器部署需要已验证的工程包CLI')
  const path = join(platform.root, `deployment-${crypto.randomUUID()}`)
  await execute(cli, ['export', '--directory', path])
  await platform.git(path, ['init', '-b', 'develop']); await platform.git(path, ['add', '.'])
  await platform.git(path, ['-c', 'user.name=部署验收', '-c', 'user.email=deploy@example.invalid', 'commit', '-m', '固定独立业务源码'])
  return path
}

/** @returns 本机空闲业务端口；不会停止或覆盖用户监听。 */
async function businessPort(): Promise<number> {
  const server = createServer()
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const address = server.address()
  await new Promise<void>((resolve, reject) => { server.close(error => { if (error) reject(error); else resolve() }) })
  if (!address || typeof address === 'string') throw new Error('业务测试端口无法确定')
  return address.port
}

/** @param request 真实HTTP @param platform 原平台 @param id 项目 @returns 已保存契约与依赖，用公开API准备既有能力夹具。 */
async function declarations(request: APIRequestContext, platform: Platform, id: string): Promise<GraphReceipt> {
  const read = async () => parseContract<GraphSnapshot>('GraphSnapshot', await (await request.get(`${platform.url}/api/v2/projects/${id}/graph`)).json())
  const graph = await read()
  const catalog = parseContract<DefinitionCatalog>('DefinitionCatalog', await (await request.get(platform.url + '/api/v1/catalog')).json())
  const definition = catalog.definitions[0]?.reference
  if (!definition) throw new Error('真实MySQL目录缺失')
  const response = await request.post(`${platform.url}/api/v1/projects/${id}/canvas/middlewares`, { headers: { Origin: platform.url, 'X-Twine-Request': '1' }, data: { mutationId: crypto.randomUUID(), baseRevision: graph.project.revision, definition, displayName: '部署数据库', configuration: { runtimeProfile: 'local-compose-v1' }, position: { x: 300, y: 200 } } })
  expect(response.status()).toBe(201)
  const saved = parseContract<CanvasReceipt>('CanvasReceipt', await response.json())
  const service = saved.project.nodes[0]; const mysql = saved.canvas.middlewares[0]
  if (!service || !mysql) throw new Error('真实服务或数据库声明缺失')
  const edited = await request.post(`${platform.url}/api/v2/projects/${id}/graph/edits`, { headers: { Origin: platform.url, 'X-Twine-Request': '1' }, data: { mutationId: crypto.randomUUID(), baseRevision: saved.project.revision, operations: [{ type: 'service.setContract', serviceId: service.id, document: SERVICE_CONTRACT_EXAMPLE }, { type: 'binding.create', source: { componentId: service.id, portId: 'database' }, target: { componentId: mysql.id, portId: 'mysql' }, databaseName: 'browser_demo', purpose: 'readWrite' }] } })
  expect(edited.status()).toBe(200)
  return parseContract<GraphReceipt>('GraphReceipt', await edited.json())
}

/** @param request 真实HTTP @param route 原服务路由 @returns 尚未发生失败的独立运行快照。 */
async function deployment(request: APIRequestContext, route: string): Promise<DeploySnapshot> {
  const response = await request.get(route + '/deployment')
  expect(response.ok()).toBe(true)
  const value = parseContract<DeploySnapshot>('DeploySnapshot', await response.json())
  if (value.operation?.state === 'failed' || value.operation?.state === 'needs_attention') throw new Error(`真实浏览器部署未完成：${value.operation.code} ${value.operation.message}`)
  return value
}

test('审阅固定候选后部署，受理响应丢失保持原键；独立入口与控制面重启保留原服务', async ({ page, platform, request }) => {
  test.setTimeout(540000)
  const path = await repository(platform); const before = await platform.fingerprint(path)
  await page.goto(platform.url); await page.getByLabel('新项目名称').fill('独立服务部署验收'); await page.getByRole('button', { name: '创建项目' }).click()
  await page.getByLabel('Git 工作区路径').fill(path); await page.getByLabel('服务显示名称').fill('部署服务'); await page.getByRole('button', { name: '加入画布' }).click()
  await expect(page.locator('.react-flow__node-service')).toHaveCount(1)
  const id = await page.locator('.inspector-foot code').innerText()
  const saved = await declarations(request, platform, id)
  const service = saved.project.nodes[0]
  if (!service) throw new Error('已保存的服务缺失')
  await page.reload(); await page.getByRole('button', { name: /独立服务部署验收/ }).click()
  await page.getByRole('button', { name: '配置部署数据库' }).click()
  page.on('dialog', dialog => { void dialog.accept() })
  await page.getByRole('button', { name: '确认运行 MySQL' }).click()
  await expect(page.getByLabel('MySQL运行事实')).toContainText('就绪', { timeout: 140000 })
  await page.getByRole('button', { name: '查看部署服务源码版本' }).click()
  await page.getByRole('button', { name: '选择当前 HEAD' }).click(); await page.getByRole('button', { name: '确认固定 commit' }).click()
  const buildRoute = `${platform.url}/api/v3/projects/${id}/services/${service.id}`
  await page.getByRole('button', { name: '检查并构建固定 commit' }).click()
  await expectBuiltCandidate(page, request, buildRoute)
  await page.getByLabel('业务入口端口').fill(String(await businessPort()))
  await page.getByRole('button', { name: '审阅部署' }).click()
  await expect(page.getByLabel('部署预览')).toBeVisible()
  const route = `${platform.url}/api/v4/projects/${id}/services/${service.id}`
  expect((await deployment(request, route)).target).toBeNull()
  const commands: DeployCommand[] = []
  await page.route('**/api/v4/projects/*/services/*/deployments', async request => {
    commands.push(parseContract<DeployCommand>('DeployCommand', request.request().postDataJSON()))
    if (commands.length === 1) { const response = await request.fetch(); expect(response.status()).toBe(202); await request.abort('failed') } else await request.continue()
  })
  await page.getByRole('button', { name: '确认部署到本机' }).click()
  await page.getByRole('button', { name: '重试原部署请求' }).click()
  await expect.poll(() => commands.length).toBe(2); expect(commands[1]).toEqual(commands[0])
  await expect.poll(async () => (await deployment(request, route)).observation?.ready, { timeout: 150000 }).toBe(true)
  const ready = await deployment(request, route)
  await expect(page.getByRole('link', { name: '打开业务入口 ↗' })).toHaveAttribute('href', ready.observation?.entryUrl ?? '')
  const response = await request.get((ready.observation?.entryUrl ?? '') + '/readyz'); expect(response.status()).toBe(200)
  await platform.restart(); await page.reload(); await page.getByRole('button', { name: /独立服务部署验收/ }).click(); await page.getByRole('button', { name: '查看部署服务源码版本' }).click()
  const reopened = await deployment(request, route)
  expect(reopened.target?.id).toBe(ready.target?.id); expect(reopened.observation?.containerId).toBe(ready.observation?.containerId)
  await expect(page.getByRole('link', { name: '打开业务入口 ↗' })).toBeVisible()
  await verifyLifecycle(page, request, platform.url, id, ready.observation?.entryUrl ?? '')
  expect(await platform.fingerprint(path)).toBe(before)
})
