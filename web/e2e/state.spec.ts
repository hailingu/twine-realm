/** 真实浏览器断网与平台进程重启保留草稿，恢复完整状态不再次确认编辑。 */
import { expect } from '@playwright/test'
import type { CanvasReceipt } from '../src/contract/generated'
import { parseContract } from '../src/project/api'
import { test } from './platformFixture'
import { declarePorts, prepareBinding, prepareBindingProject } from './bindingFixture'

test('真实断网、错位游标和平台重启恢复状态流，未保存布局及连线候选不丢失', async ({ page, platform, request }, testInfo) => {
  test.setTimeout(180000)
  const project = await prepareBindingProject(page, platform, `状态恢复-${crypto.randomUUID()}`)
  await declarePorts(page); await prepareBinding(page, await project.graph(), 'keep_connection_draft')
  const status = page.getByRole('status', { name: '状态流连接' })
  await expect(status).toHaveText('状态流已恢复', { timeout: 15000 })
  const previousCursor = await status.getAttribute('data-cursor')
  if (!previousCursor) throw new Error('缺少实际一致信封游标')
  const coordinate = page.getByRole('spinbutton', { name: /显式服务 X/ })
  await coordinate.fill('730')
  await page.context().setOffline(true)
  try {
    await expect(status).toContainText('断开', { timeout: 50000 })
    const current = await project.graph()
    const positions = [...current.project.nodes, ...current.graph.middlewares].map(node => ({ componentId: node.id, ...node.position, ...(node.id === current.project.nodes[0]?.id ? { x: 410 } : {}) }))
    const response = await request.put(`${platform.url}/api/v1/projects/${project.id}/canvas/layout`, { headers: { Origin: platform.url, 'X-Twine-Request': '1' }, data: { mutationId: crypto.randomUUID(), baseRevision: current.project.revision, positions } })
    expect(response.status()).toBe(200); const saved = parseContract<CanvasReceipt>('CanvasReceipt', await response.json())
    expect(saved.project.revision).not.toBe(current.project.revision)
    await expect(coordinate).toHaveValue('730')
    await expect(page.getByLabel('依赖数据库名称')).toHaveValue('keep_connection_draft')
  } finally { await page.context().setOffline(false) }
  await expect(status).toHaveText('状态流已恢复', { timeout: 45000 })
  await expect.poll(async () => BigInt(await status.getAttribute('data-cursor') ?? '0') > BigInt(previousCursor)).toBe(true)
  await expect(page.getByLabel('远端画布变更')).toBeVisible()
  await expect(coordinate).toHaveValue('730'); await expect(page.getByLabel('依赖数据库名称')).toHaveValue('keep_connection_draft')
  let streams = 0
  page.on('request', value => { if (value.url().endsWith(`/api/v5/projects/${project.id}/state/events`)) streams++ })
  await platform.restart()
  await expect.poll(() => streams, { timeout: 15000 }).toBeGreaterThan(0)
  await expect(status).toHaveText('状态流已恢复', { timeout: 15000 })
  await expect(coordinate).toHaveValue('730'); await expect(page.getByLabel('依赖数据库名称')).toHaveValue('keep_connection_draft')
  let invalidCursorSent = false
  await page.route(`**/api/v5/projects/${project.id}/state/events`, async route => {
    if (!invalidCursorSent) { invalidCursorSent = true; await route.continue({ headers: { ...route.request().headers(), 'last-event-id': '18446744073709551615' } }) }
    else await route.continue()
  })
  const conflict = page.waitForResponse(value => value.url().endsWith(`/api/v5/projects/${project.id}/state/events`) && value.status() === 409)
  await platform.restart(); await conflict
  await expect(status).toHaveText('状态流已恢复', { timeout: 20000 })
  await expect(coordinate).toHaveValue('730'); await expect(page.getByLabel('依赖数据库名称')).toHaveValue('keep_connection_draft')
  const actual = await project.graph()
  expect(actual.project.nodes[0]?.position.x).toBe(410); expect(actual.graph.bindings).toHaveLength(0)
  expect(await platform.fingerprint(project.path)).toBe(project.fingerprint)
  await page.screenshot({ path: testInfo.outputPath('state-recovered-drafts.png') })
})
