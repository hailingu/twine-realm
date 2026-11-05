/** 真实界面、SQL 和平台重启验证版本化 MySQL 配置草稿，不启动业务实例。 */
import { expect } from '@playwright/test'
import type { Page } from '@playwright/test'
import { test, type Platform } from './platformFixture'

/** @param page 页面 @param platform 真实平台 @param name 项目名称 @returns 已打开的新项目。 */
async function project(page: Page, platform: Platform, name: string) {
  await page.goto(platform.url)
  await page.getByLabel('新项目名称').fill(name)
  await page.getByRole('button', { name: '创建项目' }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
}

/** @param page 页面 @param name MySQL 显示名称 @returns 目录已核实、保持未选择运行方案的候选。 */
async function prepareMySQL(page: Page, name: string) {
  await page.getByRole('button', { name: '＋ MySQL 组件' }).click()
  await expect(page.getByRole('button', { name: '确认加入 MySQL' })).toBeEnabled()
  await page.getByLabel('MySQL 组件名称').fill(name)
}

test('MySQL 草稿规范化、配置修改、混合拖动布局与重启后恢复版本和身份', async ({ page, platform, browser }) => {
  const path = await platform.repository('catalog-service')
  const fingerprint = await platform.fingerprint(path)
  await project(page, platform, '标准组件持久化项目')
  await page.getByLabel('Git 工作区路径').fill(path)
  await page.getByLabel('服务显示名称').fill('保留的服务')
  await page.getByRole('button', { name: '加入画布' }).click()
  await expect(page.locator('.react-flow__node-service')).toHaveCount(1)
  const serviceId = await page.locator('.react-flow__node-service').getAttribute('data-id')
  await prepareMySQL(page, '订单数据库')
  await page.getByRole('button', { name: '确认加入 MySQL' }).click()
  const node = page.locator('.react-flow__node-mysql')
  await expect(node).toHaveCount(1)
  await expect(node).toContainText('运行配置未完整')
  const identity = await node.getAttribute('data-id')
  await page.getByRole('button', { name: '配置订单数据库' }).click()
  await expect(page.getByLabel('MySQL 内存 MiB')).toHaveValue('512')
  const digest = await page.locator('.definition-digest').innerText()
  await page.getByLabel('MySQL 内存 MiB').fill('1024')
  await page.getByLabel('MySQL 运行方案').selectOption('local-compose-v1')
  await page.getByRole('button', { name: '保存 MySQL 配置' }).click()
  await expect(node).toContainText('配置已完整')
  await expect(node).toContainText('尚未登记运行目标')
  const input = page.getByRole('spinbutton', { name: /订单数据库 X/ })
  const before = await input.inputValue()
  const box = await node.boundingBox(); if (!box) throw new Error('MySQL 节点不可拖动')
  await page.mouse.move(box.x+100,box.y+70); await page.mouse.down(); await page.mouse.move(box.x+175,box.y+110,{steps:12}); await page.mouse.up()
  await expect(input).not.toHaveValue(before)
  await page.getByRole('spinbutton', { name: /保留的服务 Y/ }).fill('420')
  const expectedX = await input.inputValue()
  await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('版本 4')
  await page.reload(); await page.getByRole('button', { name: /标准组件持久化项目/ }).click()
  await expect(page.getByRole('spinbutton', { name: /订单数据库 X/ })).toHaveValue(expectedX)
  await platform.restart()
  const context = await browser.newContext(); const reopened = await context.newPage()
  try {
    await reopened.goto(platform.url); await reopened.getByRole('button', { name: /标准组件持久化项目/ }).click()
    await expect(reopened.locator('.react-flow__node-mysql')).toHaveAttribute('data-id', identity ?? '')
    await expect(reopened.locator('.react-flow__node-service')).toHaveAttribute('data-id', serviceId ?? '')
    await reopened.getByRole('button', { name: '配置订单数据库' }).click()
    await expect(reopened.getByLabel('MySQL 内存 MiB')).toHaveValue('1024')
    await expect(reopened.getByLabel('MySQL 运行方案')).toHaveValue('local-compose-v1')
    await expect(reopened.locator('.definition-digest')).toHaveText(digest)
    await expect(reopened.getByRole('spinbutton', { name: /保留的服务 Y/ })).toHaveValue('420')
  } finally { await context.close() }
  expect(await platform.fingerprint(path)).toBe(fingerprint)
})

test('非法配置与服务器拒绝保留候选，响应丢失可核实原键且不重复创建', async ({ page, platform }) => {
  await project(page, platform, '组件请求恢复项目')
  await prepareMySQL(page, '保留数据库候选')
  await page.getByLabel('MySQL 内存 MiB').fill('255')
  await page.getByRole('button', { name: '确认加入 MySQL' }).click()
  await expect(page.getByRole('alert')).toContainText('配置无效')
  await expect(page.locator('.react-flow__node-mysql')).toHaveCount(0)
  await page.getByLabel('MySQL 内存 MiB').fill('1024')
  await page.route('**/canvas/middlewares', async route => {
    const input: unknown = route.request().postDataJSON()
    if (!input || typeof input !== 'object') throw new Error('缺少配置请求')
    await route.continue({ postData: JSON.stringify({ ...input, configuration: { password: 'dummy-only' } }) })
  })
  await page.getByRole('button', { name: '确认加入 MySQL' }).click()
  await expect(page.getByText('本次请求未保存', { exact: true })).toBeVisible()
  await expect(page.getByLabel('MySQL 组件名称')).toHaveValue('保留数据库候选')
  await expect(page.getByLabel('MySQL 内存 MiB')).toHaveValue('1024')
  await page.unroute('**/canvas/middlewares')
  await page.route('**/canvas/middlewares', async route => { await route.fetch(); await route.abort('failed') })
  await page.getByRole('button', { name: '确认加入 MySQL' }).click()
  await expect(page.getByText('请先核实原请求', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '核实保存结果' }).click()
  await expect(page.locator('.react-flow__node-mysql')).toHaveCount(1)
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('版本 1')
})

test('不同窗口配置冲突显式恢复，其他窗口的组件与自己的候选均保留', async ({ page, platform, browser }) => {
  await project(page, platform, '组件并发项目')
  const context = await browser.newContext(); const other = await context.newPage()
  try {
    await other.goto(platform.url); await other.getByRole('button', { name: /组件并发项目/ }).click()
    await prepareMySQL(other, '第二候选')
    await prepareMySQL(page, '先保存的数据库')
    await page.getByRole('button', { name: '确认加入 MySQL' }).click()
    await expect(page.locator('.react-flow__node-mysql')).toHaveCount(1)
    await other.getByRole('button', { name: '确认加入 MySQL' }).click()
    await expect(other.getByRole('alert')).toContainText('其他窗口更新')
    await expect(other.getByLabel('MySQL 组件名称')).toHaveValue('第二候选')
    await other.getByRole('button', { name: '载入最新版本并保留我的位置' }).click()
    await expect(other.locator('.react-flow__node-mysql')).toHaveCount(1)
    await other.getByRole('button', { name: '确认加入 MySQL' }).click()
    await expect(other.locator('.react-flow__node-mysql')).toHaveCount(2)
    await expect(other.getByRole('status', { name: '画布保存状态' })).toContainText('版本 2')
  } finally { await context.close() }
})
