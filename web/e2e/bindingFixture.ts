/** 具名连线验收先通过真实界面登记组件，契约仍需用户显式选择。 */
import { expect, type Page } from '@playwright/test'
import { parseContract } from '../src/project/api'
import type { GraphSnapshot } from '../src/contract/generated'
import type { Platform } from './platformFixture'

/** @param page 当前页面 @param platform 真实平台 @param name 独立项目名 @returns 保持源码不变的已打开项目和服务仓库。 */
export async function prepareBindingProject(page: Page, platform: Platform, name: string) {
  const path = await platform.repository(`binding-${crypto.randomUUID()}`)
  const fingerprint = await platform.fingerprint(path)
  await page.goto(platform.url); await page.getByLabel('新项目名称').fill(name)
  await page.getByRole('button', { name: '创建项目' }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
  await page.getByLabel('Git 工作区路径').fill(path); await page.getByLabel('服务显示名称').fill('显式服务')
  await page.getByRole('button', { name: '加入画布' }).click()
  await expect(page.locator('.react-flow__node-service')).toHaveCount(1)
  await page.getByRole('button', { name: '＋ MySQL 组件' }).click()
  await expect(page.getByRole('button', { name: '确认加入 MySQL' })).toBeEnabled()
  await page.getByLabel('MySQL 组件名称').fill('声明数据库')
  await page.getByRole('button', { name: '确认加入 MySQL' }).click()
  await expect(page.locator('.react-flow__node-mysql')).toHaveCount(1)
  const id = await page.locator('.inspector-foot code').innerText()
  const graph = async () => parseContract<GraphSnapshot>('GraphSnapshot', await (await page.request.get(`${platform.url}/api/v2/projects/${id}/graph`)).json())
  return { path, fingerprint, id, graph }
}

/** @param page 当前页面 @returns 显式声明示例而非从仓库猜测的服务接口。 */
export async function declarePorts(page: Page) {
  await page.getByRole('button', { name: '编辑显式服务接口契约' }).click()
  await expect(page.getByRole('textbox', { name: '显式服务 服务契约 JSON' })).toHaveValue('')
  await page.getByRole('button', { name: '载入端口示例' }).click()
  await page.getByRole('button', { name: '保存服务契约' }).click()
  await expect(page.locator('.react-flow__node-service .source[data-handleid="database"]')).toHaveCount(1)
}

/** @param page 当前页面 @param snapshot 已读取身份 @param name 数据库候选名 @returns 仍未提交的语义端点表单。 */
export async function prepareBinding(page: Page, snapshot: GraphSnapshot, name: string) {
  const source = snapshot.project.nodes[0]; const target = snapshot.graph.middlewares[0]
  if (!source || !target) throw new Error('缺少真实连线组件')
  await page.getByRole('button', { name: '建立数据库依赖', exact: true }).click()
  await page.getByLabel('依赖消费服务').selectOption(source.id)
  await page.getByLabel('依赖消费端口').selectOption('database')
  await page.getByLabel('依赖提供 MySQL').selectOption(target.id)
  await page.getByLabel('依赖提供端口').selectOption('mysql')
  await page.getByLabel('依赖数据库名称').fill(name)
}
