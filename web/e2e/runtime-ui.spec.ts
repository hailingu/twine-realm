/** 真实页面验证显式运行、未知回执恢复、关闭浏览器和停止目标，不用静态图代替运行证据。 */
import { expect, type Page, type BrowserContext } from '@playwright/test'
import type { RuntimeCommand, RuntimeReceipt, RuntimeSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'
import { test } from './platformFixture'
import { declarePorts, prepareBinding, prepareBindingProject } from './bindingFixture'

/** @param page 当前页面 @param label 明确动作 @returns 对本次确认框接受后完成点击。 */
async function confirmAction(page: Page, label: string): Promise<void> {
  await Promise.all([page.waitForEvent('dialog').then(dialog => dialog.accept()), page.getByRole('button', { name: label, exact: true }).click()])
}

/** @param context 本测试拥有的上下文 @param closed 已收到关闭事件 @param primary 主失败 @returns 清理仍尝试执行，不能以二次关闭异常覆盖主失败。 */
async function closeContext(context: BrowserContext, closed: () => boolean, primary: unknown): Promise<void> {
  try { await context.close() }
  catch (error) {
    const disposed = closed() && error instanceof Error && error.message.includes('Protocol error (Target.disposeBrowserContext): Failed to find context with id')
    if (disposed) return
    if (primary !== undefined) throw new AggregateError([primary, error], '运行界面验收及上下文清理均失败', { cause: error })
    throw error
  }
}

test('具名连线与运行事实共存，原键重试和重开保留声明，状态流不覆盖位置草稿', async ({ page, platform, browser, request }) => {
  test.setTimeout(180000)
  const projectName = `界面运行恢复项目-${crypto.randomUUID()}`
  const project = await prepareBindingProject(page, platform, projectName)
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
  let closed = false; let failure: unknown
  context.once('close', () => { closed = true })
  try {
    await test.step('新页面连接原平台', () => reopened.goto(platform.url))
    await reopened.getByRole('button', { name: projectName }).click()
    await reopened.getByRole('button', { name: '配置声明数据库' }).click()
    await test.step('等待原MySQL认证就绪事实', () => expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际运行 · 认证就绪', { timeout: 140000 }))
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(1)
    await expect(reopened.locator('.react-flow__node-mysql .target[data-handleid="mysql"]')).toHaveCount(1)
    await expect(reopened.locator('.react-flow__node-mysql .runtime-fact')).toHaveText('实际运行 · 认证就绪')
    const graph = (await project.graph()).graph
    expect(graph.bindings).toEqual(declared.bindings)
    expect(graph.serviceContracts).toEqual(declared.serviceContracts)
    const coordinate = reopened.getByRole('spinbutton', { name: /显式服务 X/ })
    await coordinate.fill('710')
    await expect(reopened.getByRole('status', { name: '画布保存状态' })).toContainText('布局有未保存的修改')
    const polled = await test.step('草稿期间等待下一份v6完整信封', () => reopened.waitForResponse(response => response.url() === `${platform.url}/api/v6/projects/${project.id}/state` && response.request().method() === 'GET' && response.ok(), { timeout: 8000 }))
    await test.step('完整信封响应体读取结束', () => polled.finished())
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(1)
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
    await test.step('明确受理原MySQL停止目标', () => confirmAction(reopened, '确认停止 MySQL'))
    await test.step('等待原MySQL停止事实', () => expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际已停止', { timeout: 30000 }))
    await test.step('重启测试平台并等待就绪', () => platform.restart())
    await test.step('平台重启后重新加载页面', () => reopened.reload())
    await reopened.getByRole('button', { name: projectName }).click()
    await reopened.getByRole('button', { name: '配置声明数据库' }).click()
    await expect(reopened.getByRole('status', { name: 'MySQL运行事实' })).toHaveText('实际已停止')
    await expect(reopened.locator('.react-flow__edge')).toHaveCount(1)
    expect(await platform.fingerprint(project.path)).toEqual(project.fingerprint)
  } catch (error) { failure = error; throw error }
  finally { await closeContext(context, () => closed, failure) }
})
