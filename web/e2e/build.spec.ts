/** 真实界面穿过SQL工作器、固定源码、断网Go检查和Docker镜像，不以模拟响应伪造成功。 */
import { expect, type Page } from '@playwright/test'
import { execFile } from 'node:child_process'
import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { promisify } from 'node:util'
import { parseContract } from '../src/project/api'
import type { BuildCommand, BuildSnapshot } from '../src/contract/generated'
import { test, type Platform } from './platformFixture'
import { expectBuiltCandidate } from './failureEvidence'

const execute = promisify(execFile)

/** @param platform 真实平台 @returns 已导出工程包并固定提交的独立测试仓库。 */
async function buildRepository(platform: Platform): Promise<{ path: string; commit: string }> {
  const cli = process.env.TWINE_TEST_PROFILE_BINARY
  if (!cli) throw new Error('浏览器构建检查必须由门禁提供真实工程包CLI')
  const path = join(platform.root, `build-${crypto.randomUUID()}`)
  await execute(cli, ['export', '--directory', path])
  await platform.git(path, ['init', '-b', 'develop']); await platform.git(path, ['add', '.'])
  await platform.git(path, ['-c', 'user.name=构建验收', '-c', 'user.email=build@example.invalid', 'commit', '-m', '独立固定源码'])
  return { path, commit: await platform.git(path, ['rev-parse', 'HEAD']) }
}

/** @param page 页面 @param platform 本机实例 @param path 已存在测试仓库 @param name 项目名 @returns 构建查询路由与已固定源码。 */
async function openBuild(page: Page, platform: Platform, path: string, name: string): Promise<string> {
  await page.goto(platform.url); await page.getByLabel('新项目名称').fill(name); await page.getByRole('button', { name: '创建项目' }).click()
  await page.getByLabel('Git 工作区路径').fill(path); await page.getByLabel('服务显示名称').fill('构建服务'); await page.getByRole('button', { name: '加入画布' }).click()
  await page.getByRole('button', { name: '查看构建服务源码版本' }).click()
  await page.getByRole('button', { name: '选择当前 HEAD' }).click(); await page.getByRole('button', { name: '确认固定 commit' }).click()
  await expect(page.getByRole('button', { name: '检查并构建固定 commit' })).toBeEnabled()
  const id = await page.locator('.inspector-foot code').innerText()
  const serviceId = await page.locator('.react-flow__node-service').getAttribute('data-id')
  if (!serviceId) throw new Error('缺少已登记的服务身份')
  return `${platform.url}/api/v3/projects/${id}/services/${serviceId}`
}

test('原键恢复真实构建，布局草稿与脏源码保持；重开/重启保留候选，相关输入改变使旧证据失效', async ({ page, platform, browser, request }) => {
  test.setTimeout(240000)
  const source = await buildRepository(platform)
  await writeFile(join(source.path, 'cmd/service/main.go'), 'uncommitted and unbuildable\n')
  const original = await platform.fingerprint(source.path)
  const route = await openBuild(page, platform, source.path, '固定源码构建验收')
  const commands: BuildCommand[] = []
  await page.route('**/api/v3/projects/*/services/*/builds', async request => {
    commands.push(parseContract<BuildCommand>('BuildCommand', request.request().postDataJSON()))
    if (commands.length === 1) { const response = await request.fetch(); expect(response.status()).toBe(202); await request.abort('failed') }
    else await request.continue()
  })
  await page.getByRole('button', { name: '检查并构建固定 commit' }).click()
  await expect(page.getByRole('button', { name: '重试原构建请求' })).toBeVisible()
  await page.getByRole('button', { name: '重试原构建请求' }).click()
  await expect.poll(() => commands.length).toBe(2)
  expect(commands[1]).toEqual(commands[0])
  const x = page.getByRole('spinbutton', { name: /构建服务 X/ }); await x.fill('777')
  await expectBuiltCandidate(page, request, route)
  await expect(x).toHaveValue('777'); await expect(page.getByLabel('画布保存状态')).toContainText('未保存')
  const value = parseContract<BuildSnapshot>('BuildSnapshot', await (await request.get(route + '/build')).json())
  expect(value.candidateCurrent).toBe(true); expect(value.candidate?.source.pinnedCommit).toBe(source.commit)
  const candidate = value.candidate
  if (!candidate) throw new Error('缺少真实镜像候选')
  const inspected: unknown = JSON.parse((await execute('docker', ['--context', candidate.environment.context, 'image', 'inspect', '--format={{json .Id}}', candidate.image.tag])).stdout)
  expect(inspected).toBe(candidate.image.id)
  expect([candidate.image.configDigest, candidate.image.manifestDigest]).toContain(inspected)
  expect(candidate.image.exportDigest).toMatch(/^sha256:[a-f0-9]{64}$/)
  expect(inspected).toBe(candidate.image.idKind === 'config' ? candidate.image.configDigest : candidate.image.manifestDigest)
  if (candidate.image.manifestDigest) expect(candidate.image.manifestMediaType).toMatch(/^application\/vnd\.(?:oci\.image|docker\.distribution\.manifest)/)
  else expect(candidate.image.idKind).toBe('config')
  expect(await platform.fingerprint(source.path)).toBe(original)
  const candidateId = value.candidate?.id
  const reopened = await browser.newPage(); await reopened.goto(platform.url); await reopened.getByRole('button', { name: /固定源码构建验收/ }).click(); await reopened.getByRole('button', { name: '查看构建服务源码版本' }).click()
  await expect(reopened.getByText('当前固定输入已通过构建')).toBeVisible()
  await platform.restart(); await reopened.reload(); await reopened.getByRole('button', { name: /固定源码构建验收/ }).click(); await reopened.getByRole('button', { name: '查看构建服务源码版本' }).click()
  const after = parseContract<BuildSnapshot>('BuildSnapshot', await (await request.get(route + '/build')).json())
  expect(after.candidate?.id).toBe(candidateId); expect(after.candidateCurrent).toBe(true)
  await platform.git(source.path, ['-c', 'user.name=构建验收', '-c', 'user.email=build@example.invalid', 'commit', '--allow-empty', '-m', '新的固定输入'])
  const next = await platform.git(source.path, ['rev-parse', 'HEAD'])
  await reopened.getByLabel('待固定完整 commit').fill(next); await reopened.getByRole('button', { name: '确认固定 commit' }).click()
  await expect(reopened.getByText('历史镜像候选 · 当前尚未确认可用')).toBeVisible()
  const changed = parseContract<BuildSnapshot>('BuildSnapshot', await (await request.get(route + '/build')).json())
  expect(changed.candidate?.id).toBe(candidateId); expect(changed.candidateCurrent).toBe(false); expect(changed.candidateCode).toBe('INPUT_CHANGED')
  await reopened.close()
})

test('取消与控制面退出保留原操作，不自动重跑业务代码或发布候选', async ({ page, platform, request }) => {
  test.setTimeout(150000)
  const source = await buildRepository(platform)
  const route = await openBuild(page, platform, source.path, '构建取消恢复验收')
  await page.getByRole('button', { name: '检查并构建固定 commit' }).click()
  await expect(page.getByRole('button', { name: '取消并核实原构建' })).toBeEnabled()
  await expect(page.getByRole('listitem').filter({ hasText: 'Go静态检查：检查中' })).toBeVisible({ timeout: 40000 })
  await page.getByRole('button', { name: '取消并核实原构建' }).click()
  await expect(page.getByLabel('构建状态')).toHaveText('已取消并收回沙箱', { timeout: 20000 })
  const cancelled = parseContract<BuildSnapshot>('BuildSnapshot', await (await request.get(route + '/build')).json())
  expect(cancelled.candidate).toBeNull(); expect(cancelled.operation?.cancelRequested).toBe(true)
  await page.getByRole('button', { name: '检查并构建固定 commit' }).click()
  await expect(page.getByRole('button', { name: '取消并核实原构建' })).toBeEnabled()
  await expect(page.getByRole('listitem').filter({ hasText: 'Go静态检查：检查中' })).toBeVisible({ timeout: 40000 })
  await platform.restart(); await page.reload(); await page.getByRole('button', { name: /构建取消恢复验收/ }).click(); await page.getByRole('button', { name: '查看构建服务源码版本' }).click()
  await expect(page.getByLabel('构建状态')).toHaveText('原结果需核实', { timeout: 20000 })
  const interrupted = parseContract<BuildSnapshot>('BuildSnapshot', await (await request.get(route + '/build')).json())
  expect(interrupted.candidate).toBeNull(); expect(interrupted.operation?.id).not.toBe(cancelled.operation?.id)
  await page.getByRole('button', { name: '取消并核实原构建' }).click()
  await expect(page.getByLabel('构建状态')).toHaveText('已取消并收回沙箱', { timeout: 20000 })
})
