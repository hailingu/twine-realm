/** 真实浏览器、SQL 和重启验证声明连线，不把连线误称为已运行的数据库连接。 */
import { expect } from '@playwright/test'
import { test } from './platformFixture'
import { declarePorts, prepareBinding, prepareBindingProject } from './bindingFixture'
import { expectRestoredEdge } from './failureEvidence'

test('显式接口、具名拖线、失败重连、删除与平台重启恢复稳定关系身份', async ({ page, platform, browser }) => {
  const projectName = `具名端口持久化项目-${crypto.randomUUID()}`
  const project = await prepareBindingProject(page, platform, projectName)
  await expect(page.locator('.react-flow__node-service')).toContainText('未定义契约')
  await expect(page.locator('.react-flow__node-service .react-flow__handle')).toHaveCount(0)
  await declarePorts(page)
  await expect(page.locator('.react-flow__node-service')).toContainText('缺少依赖绑定')
  await page.getByRole('spinbutton', { name: /显式服务 X/ }).fill('40')
  await page.getByRole('spinbutton', { name: /声明数据库 X/ }).fill('620')
  await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('status')).toContainText('版本 4')
  await page.getByRole('button', { name: '适应画布' }).click()
  const source = page.locator('.react-flow__node-service .source[data-handleid="database"]')
  const target = page.locator('.react-flow__node-mysql .target[data-handleid="mysql"]')
  await expect(source).toBeInViewport(); await expect(target).toBeInViewport()
  await expect.poll(() => source.evaluate(element => { const rect = element.getBoundingClientRect(); return document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)?.getAttribute('data-handleid') })).toBe('database')
  await expect.poll(() => target.evaluate(element => { const rect = element.getBoundingClientRect(); return document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)?.getAttribute('data-handleid') })).toBe('mysql')
  await source.dragTo(target)
  await expect(page.getByLabel('依赖消费端口')).toHaveValue('database')
  await expect(page.getByLabel('依赖提供端口')).toHaveValue('mysql')
  await expect(page.locator('.react-flow__edge')).toHaveCount(0)
  await page.getByLabel('依赖数据库名称').fill('app_canvas')
  await page.getByRole('button', { name: '确认保存依赖' }).click()
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)
  await expect(page.locator('.react-flow__edge-text')).toContainText('声明未应用')
  const accepted = await project.graph(); const binding = accepted.graph.bindings[0]
  if (!binding) throw new Error('缺少 SQL 保存的真实关系')
  expect(accepted.graph.serviceContracts[0]?.document.interfaces).toHaveLength(2)
  await page.getByRole('button', { name: '重连app_canvas' }).click()
  await page.getByLabel('依赖数据库名称').fill('Bad-Name')
  await page.getByRole('button', { name: '确认重连依赖' }).click()
  await expect(page.getByRole('alert')).toContainText('数据库名称')
  expect((await project.graph()).graph.bindings).toEqual([binding])
  await page.getByLabel('依赖数据库名称').fill('app_changed')
  await page.getByLabel('依赖数据库用途').selectOption('readOnly')
  await page.getByRole('button', { name: '确认重连依赖' }).click()
  await expect(page.getByRole('button', { name: '重连app_changed' })).toBeVisible()
  const reconnected = (await project.graph()).graph.bindings[0]
  expect(reconnected?.id).toBe(binding.id); expect(reconnected?.databaseId).not.toBe(binding.databaseId)
  await platform.restart()
  const context = await browser.newContext(); const reopened = await context.newPage()
  try {
    await reopened.goto(platform.url); await reopened.getByRole('button', { name: projectName }).click()
    await expectRestoredEdge(reopened, binding.id)
    await reopened.getByRole('button', { name: '查看显式服务源码版本' }).click()
    await expectRestoredEdge(reopened, binding.id)
    await expect(reopened.locator('.react-flow__edge-text')).toContainText('app_changed')
    await expect(reopened.locator('.react-flow__node-service .react-flow__handle')).toHaveCount(3)
    const acceptedDialog = reopened.waitForEvent('dialog').then(dialog => dialog.accept())
    await reopened.getByRole('button', { name: '移除app_changed依赖声明' }).click(); await acceptedDialog
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(0)
    await expect(reopened.locator('.react-flow__node-service')).toContainText('缺少依赖绑定')
  } finally { await context.close() }
  expect(await platform.fingerprint(project.path)).toBe(project.fingerprint)
})

test('已落库响应丢失时核实原键，非法角色重连保留原边和候选', async ({ page, platform }) => {
  const project = await prepareBindingProject(page, platform, '连线请求恢复项目')
  await declarePorts(page); await prepareBinding(page, await project.graph(), 'app_recover')
  let originalKey = ''
  await page.route('**/graph/edits', async route => {
    const input: unknown = route.request().postDataJSON()
    if (input && typeof input === 'object' && 'mutationId' in input && typeof input.mutationId === 'string') originalKey = input.mutationId
    await route.fetch(); await route.abort('failed')
  })
  await page.getByRole('button', { name: '确认保存依赖' }).click()
  await expect(page.getByText('请先核实原请求', { exact: true })).toBeVisible()
  await expect(page.getByLabel('依赖数据库名称')).toHaveValue('app_recover')
  await expect(page.getByLabel('依赖数据库名称')).toBeDisabled()
  await page.unroute('**/graph/edits')
  const receiptRead = page.waitForRequest(`**/api/v2/graph-mutations/${originalKey}`)
  await page.getByRole('button', { name: '核实保存结果' }).click(); await receiptRead
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)
  const saved = (await project.graph()).graph.bindings
  await page.getByRole('button', { name: '重连app_recover' }).click()
  await page.getByLabel('依赖数据库名称').fill('app_failed_candidate')
  await page.route('**/graph/edits', async route => {
    const input: unknown = route.request().postDataJSON()
    if (!input || typeof input !== 'object' || !('operations' in input) || !Array.isArray(input.operations)) throw new Error('缺少原子操作')
    const first: unknown = input.operations[0]
    if (!first || typeof first !== 'object' || !('source' in first)) throw new Error('缺少连线端点')
    const altered = { ...first, source: { componentId: saved[0]?.source.componentId, portId: 'http-v1' } }
    await route.continue({ postData: JSON.stringify({ ...input, operations: [altered] }) })
  })
  await page.getByRole('button', { name: '确认重连依赖' }).click()
  await expect(page.getByText('本次请求未保存', { exact: true })).toBeVisible()
  await expect(page.getByLabel('依赖数据库名称')).toHaveValue('app_failed_candidate')
  expect((await project.graph()).graph.bindings).toEqual(saved)
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)
})

test('另一窗口改动产生版本冲突时保留连线候选并载入完整新图', async ({ page, platform, browser }) => {
  const project = await prepareBindingProject(page, platform, '连线并发项目')
  await declarePorts(page)
  const context = await browser.newContext(); const other = await context.newPage()
  try {
    await other.goto(platform.url); await other.getByRole('button', { name: /连线并发项目/ }).click()
    await prepareBinding(other, await project.graph(), 'app_other_candidate')
    await page.getByRole('spinbutton', { name: /显式服务 Y/ }).fill('600')
    await page.getByRole('button', { name: '保存布局' }).click()
    await expect(page.getByRole('status')).toContainText('版本 4')
    await other.getByRole('button', { name: '确认保存依赖' }).click()
    await expect(other.getByText('本次请求未保存', { exact: true })).toBeVisible()
    await expect(other.getByLabel('依赖数据库名称')).toHaveValue('app_other_candidate')
    await other.getByRole('button', { name: '载入最新版本并保留我的位置' }).click()
    await expect(other.getByRole('status')).toContainText('布局有未保存的修改')
    await expect(other.getByLabel('依赖数据库名称')).toHaveValue('app_other_candidate')
    await other.getByRole('button', { name: '还原布局' }).click()
    await expect(other.getByRole('status')).toContainText('版本 4')
    await other.getByRole('button', { name: '确认保存依赖' }).click()
    await expect(other.locator('.react-flow__edge')).toHaveCount(1)
    expect((await project.graph()).graph.bindings).toHaveLength(1)
  } finally { await context.close() }
})
