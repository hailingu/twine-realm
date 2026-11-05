/** 真实页面验证显式运行、未知回执恢复、关闭浏览器和停止目标，不用静态图代替运行证据。 */
import { expect, type Page } from '@playwright/test'
import type { RuntimeCommand, RuntimeReceipt, RuntimeSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'
import { test } from './platformFixture'
import { declarePorts, prepareBinding, prepareBindingProject } from './bindingFixture'

/** @param page 当前页面 @param label 明确动作 @returns 对本次确认框接受后完成点击。 */
async function confirmAction(page: Page, label: string): Promise<void> {
  await Promise.all([page.waitForEvent('dialog').then(dialog => dialog.accept()), page.getByRole('button', { name: label, exact: true }).click()])
}

test('具名连线与运行事实共存，原键重试和重开保留声明，轮询不覆盖位置草稿', async ({ page, platform, browser, request }) => {
  test.setTimeout(180000)
  const project = await prepareBindingProject(page, platform, '界面运行恢复项目')
  await declarePorts(page)
  await prepareBinding(page, await project.graph(), 'app_runtime')
  await page.getByRole('button', { name: '确认保存依赖' }).click()
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)
  const declared = (await project.graph()).graph
  await page.getByRole('button', { name: '配置声明数据库' }).click()
  await page.getByLabel('MySQL 运行方案').selectOption('local-compose-v1')
  await page.getByRole('button', { name: '保存 MySQL 配置' }).click()
  await expect(page.getByRole('button', { name: '确认运行 MySQL' })).toBeEnabled()
  const commands: RuntimeCommand[] = []
  let accepted: RuntimeReceipt | undefined
  await page.route('**/runtime/*/targets', async route => {
    commands.push(parseContract<RuntimeCommand>('RuntimeCommand', route.request().postDataJSON()))
    if (commands.length === 1) {
      const response = await route.fetch()
      expect(response.status()).toBe(202)
      accepted = parseContract<RuntimeReceipt>('RuntimeReceipt', await response.json())
      await route.abort('failed')
    } else await route.continue()
  })
  await confirmAction(page, '确认运行 MySQL')
  await expect(page.getByText('运行请求结果尚未确认', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '重试原运行请求' }).click()
  await expect.poll(() => commands.length).toBe(2)
  expect(commands[1]).toEqual(commands[0])
  if (!accepted) throw new Error('真实运行请求未受理')
  const original = accepted
  await page.close()
  const context = await browser.newContext()
  const reopened = await context.newPage()
  try {
    await reopened.goto(platform.url)
    await reopened.getByRole('button', { name: /界面运行恢复项目/ }).click()
    await reopened.getByRole('button', { name: '配置声明数据库' }).click()
    await expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际运行 · 认证就绪', { timeout: 140000 })
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(1)
    await expect(reopened.locator('.react-flow__node-mysql .target[data-handleid="mysql"]')).toHaveCount(1)
    await expect(reopened.locator('.react-flow__node-mysql .runtime-fact')).toHaveText('实际运行 · 认证就绪')
    const graph = (await project.graph()).graph
    expect(graph.bindings).toEqual(declared.bindings)
    expect(graph.serviceContracts).toEqual(declared.serviceContracts)
    const coordinate = reopened.getByRole('spinbutton', { name: /显式服务 X/ })
    await coordinate.fill('710')
    await expect(reopened.getByRole('status', { name: '画布保存状态' })).toContainText('布局有未保存的修改')
    const polled = await reopened.waitForResponse(response => response.url() === `${platform.url}/api/v1/projects/${project.id}/runtime` && response.request().method() === 'GET' && response.ok())
    await polled.finished()
    await expect(coordinate).toHaveValue('710')
    await expect(reopened.getByRole('status', { name: '画布保存状态' })).toContainText('布局有未保存的修改')
    await reopened.getByRole('button', { name: '还原布局' }).click()
    const response = await request.get(`${platform.url}/api/v1/projects/${original.target.projectId}/runtime`)
    const value = parseContract<RuntimeSnapshot>('RuntimeSnapshot', await response.json())
    expect(value.operation?.id).toBe(original.operationId)
    expect(value.generation).toBe('1')
    await reopened.getByText('持久运行事件', { exact: true }).click()
    await reopened.getByRole('button', { name: '读取运行事件' }).click()
    await expect(reopened.locator('.runtime-events li').first()).toContainText('target_accepted')
    await confirmAction(reopened, '确认停止 MySQL')
    await expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际已停止', { timeout: 30000 })
    await platform.restart()
    await reopened.reload()
    await reopened.getByRole('button', { name: /界面运行恢复项目/ }).click()
    await reopened.getByRole('button', { name: '配置声明数据库' }).click()
    await expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际已停止')
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(1)
    expect(await platform.fingerprint(project.path)).toEqual(project.fingerprint)
  } finally { await context.close() }
})
