/** 经过真实页面、HTTP、Git 与 MySQL 验证 Issue #1 的用户可见闭环。 */
import { expect } from '@playwright/test'
import type { Page } from '@playwright/test'
import { mkdir, chmod } from 'node:fs/promises'
import { join } from 'node:path'
import { test } from './platformFixture'
import type { Platform } from './platformFixture'

test.beforeEach(({ page }) => {
  page.on('pageerror', error => { throw new Error(`浏览器执行失败：${error.message}`) })
})

/** @param page 真实页面 @param platform 测试平台 @param name 项目名称 @returns 已打开的空画布。 */
async function createProject(page: Page, platform: Platform, name: string) {
  await page.goto(platform.url)
  await expect(page.getByLabel('新项目名称')).toBeVisible()
  await page.getByLabel('新项目名称').fill(name)
  await page.getByRole('button', { name: '创建项目' }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
}

/** @param page 真实页面 @param path 本机仓库路径 @param name 可选名称 @returns 已完成登记交互。 */
async function register(page: Page, path: string, name = '') {
  await page.getByLabel('Git 工作区路径').fill(path)
  await page.getByLabel('服务显示名称').fill(name)
  await page.getByRole('button', { name: '加入画布' }).click()
}

test('创建、只读登记、实际拖动、保存与平台重启后恢复稳定身份和位置', async ({ page, platform, browser }) => {
  const path = await platform.repository('first')
  const fingerprint = await platform.fingerprint(path)
  await createProject(page, platform, '第一个本地项目')
  await register(page, path, '订单服务')
  const node = page.locator('.react-flow__node')
  await expect(node).toHaveCount(1)
  await expect(node.getByText('订单服务', { exact: true })).toBeVisible()
  const identity = await node.getAttribute('data-id')
  const position = page.getByRole('spinbutton', { name: /订单服务 X/ })
  const before = await position.inputValue()
  const box = await node.boundingBox()
  if (!box) throw new Error('节点没有可拖动的尺寸')
  await page.mouse.move(box.x + 110, box.y + 75); await page.mouse.down(); await page.mouse.move(box.x + 210, box.y + 135, { steps: 15 }); await page.mouse.up()
  await expect(position).not.toHaveValue(before)
  await expect(page.getByRole('button', { name: '保存布局' })).toBeEnabled()
  const expectedX = await position.inputValue()
  await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('已保存到本机')
  await page.reload()
  await page.getByRole('button', { name: /第一个本地项目/ }).click()
  await expect(page.getByRole('spinbutton', { name: /订单服务 X/ })).toHaveValue(expectedX)
  await platform.restart()
  const context = await browser.newContext()
  const reopened = await context.newPage()
  await reopened.goto(platform.url); await reopened.getByRole('button', { name: /第一个本地项目/ }).click()
  await expect(reopened.locator('.react-flow__node')).toHaveAttribute('data-id', identity ?? '')
  await expect(reopened.getByRole('spinbutton', { name: /订单服务 X/ })).toHaveValue(expectedX)
  await expect(reopened.locator('.service-path')).toHaveText(path)
  if (process.env.TWINE_TEST_SCREENSHOT) await reopened.screenshot({ path: process.env.TWINE_TEST_SCREENSHOT, fullPage: true })
  await context.close()
  expect(await platform.fingerprint(path)).toBe(fingerprint)
})

test('错误路径、不可读、空仓库、重复及同名仓库有明确结果', async ({ page, platform }) => {
  await createProject(page, platform, '仓库校验项目')
  await register(page, join(platform.root, 'missing'))
  await expect(page.getByRole('alert')).toContainText('不存在或不可读取')
  await expect(page.locator('.react-flow__node')).toHaveCount(0)
  const plain = join(platform.root, 'plain'); await mkdir(plain)
  await register(page, plain); await expect(page.getByRole('alert')).toContainText('不是可读取的 Git 工作区')
  const unreadable = join(platform.root, 'unreadable'); await mkdir(unreadable); await chmod(unreadable, 0)
  try { await register(page, unreadable); await expect(page.getByRole('alert')).toContainText('不可读取') }
  finally { await chmod(unreadable, 0o755) }
  const empty = await platform.repository('empty', false)
  await register(page, empty); await expect(page.locator('.react-flow__node')).toHaveCount(1)
  await expect(page.locator('.service-card')).toContainText('暂无源提交')
  await expect(page.locator('.service-card')).toContainText('待配置')
  await register(page, empty); await expect(page.getByRole('alert')).toContainText('已加入此项目')
  await expect(page.locator('.react-flow__node')).toHaveCount(1)
  const second = await platform.repository('same-name')
  await register(page, second); await expect(page.locator('.react-flow__node')).toHaveCount(2)
  await expect(page.locator('.service-path').filter({ hasText: empty })).toHaveCount(1)
  await expect(page.locator('.service-path').filter({ hasText: second })).toHaveCount(1)
})

test('保存响应丢失时保留草稿并核实原回执，不重复提交', async ({ page, platform }) => {
  const path = await platform.repository('uncertain')
  await createProject(page, platform, '保存恢复项目'); await register(page, path, '恢复服务')
  const input = page.getByRole('spinbutton', { name: /恢复服务 X/ })
  await input.fill('540')
  let intercepted = false
  await page.route('**/api/v1/projects/*/canvas/layout', async route => {
    if (intercepted) { await route.continue(); return }
    intercepted = true
    await route.fetch(); await route.abort('failed')
  })
  await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('alert')).toContainText('请先核实原请求')
  await expect(input).toHaveValue('540')
  await page.getByRole('button', { name: '核实保存结果' }).click()
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('已保存到本机 · 版本 2')
  await expect(input).toHaveValue('540')
  await page.reload(); await page.getByRole('button', { name: /保存恢复项目/ }).click()
  await expect(page.getByRole('spinbutton', { name: /恢复服务 X/ })).toHaveValue('540')
})

test('未送达保存用原请求重试；版本冲突显式重放草稿', async ({ page, platform, browser }) => {
  const path = await platform.repository('retry')
  await createProject(page, platform, '重试与冲突项目'); await register(page, path, '并发服务')
  const input = page.getByRole('spinbutton', { name: /并发服务 X/ })
  await input.fill('610')
  await page.route('**/api/v1/projects/*/canvas/layout', route => route.abort('failed'))
  await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('alert')).toContainText('请先核实原请求')
  await page.getByRole('button', { name: '核实保存结果' }).click()
  await expect(page.getByRole('alert')).toContainText('尚未找到该请求')
  await expect(input).toHaveValue('610')
  await page.unroute('**/api/v1/projects/*/canvas/layout')
  await page.getByRole('button', { name: '重试原请求' }).click()
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('已保存到本机 · 版本 2')
  const context = await browser.newContext(); const other = await context.newPage()
  await other.goto(platform.url); await other.getByRole('button', { name: /重试与冲突项目/ }).click()
  await input.fill('710'); await page.getByRole('button', { name: '保存布局' }).click()
  await expect(page.getByRole('status', { name: '画布保存状态' })).toContainText('版本 3')
  await other.getByRole('spinbutton', { name: /并发服务 X/ }).fill('820')
  await other.getByRole('button', { name: '保存布局' }).click()
  await expect(other.getByRole('alert')).toContainText('其他窗口更新')
  await expect(other.getByRole('spinbutton', { name: /并发服务 X/ })).toHaveValue('820')
  await other.getByRole('button', { name: '载入最新版本并保留我的位置' }).click()
  await other.getByRole('button', { name: '保存布局' }).click()
  await expect(other.getByRole('status', { name: '画布保存状态' })).toContainText('版本 4')
  await context.close()
})
