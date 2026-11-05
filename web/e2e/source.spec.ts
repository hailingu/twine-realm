/** 通过真实页面、Git、MySQL 与进程重启验证源码固定闭环。 */
import { expect } from '@playwright/test'
import type { Page } from '@playwright/test'
import { rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { test, type Platform } from './platformFixture'

/** @param page 页面 @param platform 真实平台 @param name 项目名 @param path 真实仓库 @returns 已打开的源码详情。 */
async function openSource(page: Page, platform: Platform, name: string, path: string) {
  await page.goto(platform.url)
  await page.getByLabel('新项目名称').fill(name)
  await page.getByRole('button', { name: '创建项目' }).click()
  await page.getByLabel('Git 工作区路径').fill(path)
  await page.getByLabel('服务显示名称').fill('源码服务')
  await page.getByRole('button', { name: '加入画布' }).click()
  await page.getByRole('button', { name: '查看源码服务源码版本' }).click()
  await expect(page.getByText('分支 develop', { exact: true })).toBeVisible()
}

test('固定只读源码，后续 HEAD 与脏工作区不漂移，重开及重启恢复固定关联', async ({ page, platform, browser }) => {
  const path = await platform.repository('source-version')
  const head = await platform.git(path, ['rev-parse', 'HEAD'])
  const before = await platform.fingerprint(path)
  await openSource(page, platform, '源码持久化项目', path)
  await expect(page.getByTestId('pinned-commit')).toHaveText('尚未固定')
  await page.getByRole('button', { name: '选择当前 HEAD' }).click()
  await expect(page.getByTestId('pinned-commit')).toHaveText('尚未固定')
  await page.getByRole('button', { name: '确认固定 commit' }).click()
  await expect(page.getByTestId('pinned-commit')).toHaveText(head)
  expect(await platform.fingerprint(path)).toBe(before)
  const serviceId = await page.locator('.react-flow__node').getAttribute('data-id')
  await platform.git(path, ['-c', 'user.name=测试', '-c', 'user.email=test@example.invalid', 'commit', '--allow-empty', '-m', '新源码'])
  const next = await platform.git(path, ['rev-parse', 'HEAD'])
  await platform.git(path, ['checkout', '--detach'])
  await writeFile(join(path, 'draft.txt'), '未提交的业务草稿\n')
  const changed = await platform.fingerprint(path)
  await page.getByRole('button', { name: '刷新仓库状态' }).click()
  await expect(page.getByText('游离 HEAD', { exact: true })).toBeVisible()
  await expect(page.getByText('工作区有未提交修改', { exact: true })).toBeVisible()
  await expect(page.getByTestId('pinned-commit')).toHaveText(head)
  await expect(page.getByLabel('待固定完整 commit')).toHaveValue(head)
  await expect(page.locator('.git-observation')).toContainText(next)
  await page.reload(); await page.getByRole('button', { name: /源码持久化项目/ }).click()
  await page.getByRole('button', { name: '查看源码服务源码版本' }).click()
  await expect(page.getByTestId('pinned-commit')).toHaveText(head)
  await platform.restart()
  const context = await browser.newContext(); const reopened = await context.newPage()
  try {
    await reopened.goto(platform.url); await reopened.getByRole('button', { name: /源码持久化项目/ }).click()
    await reopened.getByRole('button', { name: '查看源码服务源码版本' }).click()
    await expect(reopened.getByTestId('pinned-commit')).toHaveText(head)
    await expect(reopened.locator('.react-flow__node')).toHaveAttribute('data-id', serviceId ?? '')
  } finally { await context.close() }
  expect(await platform.fingerprint(path)).toBe(changed)
})

test('源码响应丢失可核实原键，仓库失联仍保留固定记录', async ({ page, platform }) => {
  const path = await platform.repository('source-recovery')
  const head = await platform.git(path, ['rev-parse', 'HEAD'])
  await openSource(page, platform, '源码原键恢复项目', path)
  await page.getByRole('button', { name: '选择当前 HEAD' }).click()
  await page.route('**/api/v1/projects/*/services/*/source', async route => {
    if (route.request().method() !== 'PUT') { await route.continue(); return }
    await route.fetch(); await route.abort('failed')
  })
  await page.getByRole('button', { name: '确认固定 commit' }).click()
  await expect(page.getByText('请先核实原请求', { exact: true })).toBeVisible()
  await expect(page.getByLabel('待固定完整 commit')).toHaveValue(head)
  await page.getByRole('button', { name: '核实保存结果' }).click()
  await expect(page.getByTestId('pinned-commit')).toHaveText(head)
  await expect(page.getByRole('status')).toContainText('版本 2')
  await rename(path, path+'-unavailable')
  await page.getByRole('button', { name: '刷新仓库状态' }).click()
  await expect(page.getByRole('alert')).toContainText('已有固定记录保留')
  await expect(page.getByTestId('pinned-commit')).toHaveText(head)
})
