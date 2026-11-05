/** 真实平台、Compose、私网与MySQL验证持久操作，不用mock证明资源和数据恢复。 */
import { expect, type APIRequestContext } from '@playwright/test'
import { execFile } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { promisify } from 'node:util'
import { setTimeout as delay } from 'node:timers/promises'
import type { CanvasReceipt, DefinitionCatalog, Receipt, RuntimeCommand, RuntimeReceipt, RuntimeSnapshot } from '../src/contract/generated'
import { parseContract } from '../src/project/api'
import { test } from './platformFixture'

const execute = promisify(execFile)

/** @param request 测试HTTP端口 @param origin 真实平台 @param path 明确API @param body 原意图 @param status 受理状态 @param schema 发布契约 @returns 已校验响应。 */
async function post<T>(request: APIRequestContext, origin: string, path: string, body: object, status: number, schema: string): Promise<T> {
  const response = await request.post(origin + path, { data: body, headers: { Origin: origin, 'X-Twine-Request': '1' } })
  expect(response.status()).toBe(status)
  return parseContract<T>(schema, await response.json())
}

/** @param request HTTP端口 @param origin 真实平台 @param id 项目 @returns 已验证当前运行快照。 */
async function read(request: APIRequestContext, origin: string, id: string): Promise<RuntimeSnapshot> {
  const response = await request.get(`${origin}/api/v1/projects/${id}/runtime`)
  expect(response.ok()).toBe(true)
  return parseContract<RuntimeSnapshot>('RuntimeSnapshot', await response.json())
}

/** @param request HTTP端口 @param origin 真实平台 @param id 项目 @param desired 目标 @returns 通过认证或实际停止验证的终态。 */
async function waitForRuntime(request: APIRequestContext, origin: string, id: string, desired: 'running' | 'stopped'): Promise<RuntimeSnapshot> {
  const deadline = Date.now() + 140000
  while (Date.now() < deadline) {
    const value = await read(request, origin, id)
    if (value.operation?.state === 'needs_attention' || value.operation?.state === 'failed') {
      throw new Error(`实际运行未完成：${value.operation.code}；${value.operation.message}；步骤 ${JSON.stringify(value.operation.steps)}`)
    }
    if (value.target?.desired === desired && value.operation?.state === 'succeeded' && value.freshness === 'fresh' && value.observation && value.observation.running === (desired === 'running') && (desired === 'stopped' || value.observation.ready)) return value
    await delay(500)
  }
  throw new Error('真实运行操作未在期限内完成')
}

/** @param value 真实快照 @param sql 本测试固定SQL @returns 临时独立MySQL中的见证结果。 */
async function witness(value: RuntimeSnapshot, sql: string): Promise<string> {
  const context = process.env.TWINE_TEST_DOCKER_CONTEXT
  const daemon = process.env.TWINE_TEST_DOCKER_DAEMON_ID
  const installation = process.env.TWINE_TEST_RUNTIME_INSTALLATION_ID
  const container = value.observation?.containerId
  if (!context || !/^[A-Za-z0-9_.-]+$/.test(context) || !daemon || !installation || !container || !/^[0-9a-f]{64}$/.test(container)) throw new Error('临时Docker验证环境缺失')
  const info = await execute('docker', ['--context', context, 'info', '--format', '{{.ID}}'], { timeout: 15000 })
  expect(info.stdout.trim()).toBe(daemon)
  const identity = await execute('docker', ['--context', context, 'container', 'inspect', container, '--format', '{{json .Config.Labels}}'], { timeout: 15000 })
  const labels: unknown = JSON.parse(identity.stdout)
  expect(labels).toMatchObject({ 'io.twine-realm.installation': installation, 'io.twine-realm.project': value.projectId, 'io.twine-realm.resource': value.target?.resource.id })
  const result = await execute('docker', ['--context', context, 'exec', container, 'mysql', '--defaults-extra-file=/run/secrets/client_options', '--batch', '--skip-column-names', '--execute=' + sql], { timeout: 15000 })
  return result.stdout.trim()
}

test('真实MySQL受理、原键重放、控制面重启和停止后恢复均保持数据身份', async ({ request, platform }) => {
  test.setTimeout(240000)
  const created = await post<Receipt>(request, platform.url, '/api/v1/projects', { mutationId: randomUUID(), name: '持久运行验证' }, 201, 'Receipt')
  const catalog = parseContract<DefinitionCatalog>('DefinitionCatalog', await (await request.get(platform.url + '/api/v1/catalog')).json())
  const definition = catalog.definitions[0]?.reference
  expect(definition).toBeDefined()
  const saved = await post<CanvasReceipt>(request, platform.url, `/api/v1/projects/${created.project.id}/canvas/middlewares`, { mutationId: randomUUID(), baseRevision: created.project.revision, displayName: '运行数据库', definition, configuration: { runtimeProfile: 'local-compose-v1' }, position: { x: 320, y: 240 } }, 201, 'CanvasReceipt')
  const component = saved.canvas.middlewares[0]
  if (!component) throw new Error('实际MySQL声明未保存')
  const path = `/api/v1/projects/${created.project.id}/runtime/${component.id}/targets`
  const command: RuntimeCommand = { mutationId: randomUUID(), baseRevision: saved.project.revision, baseGeneration: '0', desired: 'running' }
  const accepted = await post<RuntimeReceipt>(request, platform.url, path, command, 202, 'RuntimeReceipt')
  const repeated = await post<RuntimeReceipt>(request, platform.url, path, command, 202, 'RuntimeReceipt')
  expect(repeated.operationId).toBe(accepted.operationId); expect(repeated.target).toEqual(accepted.target); expect(repeated.replayed).toBe(true)
  const running = await waitForRuntime(request, platform.url, created.project.id, 'running')
  await witness(running, "CREATE DATABASE runtime_witness; CREATE TABLE runtime_witness.marker(value VARCHAR(32)); INSERT INTO runtime_witness.marker VALUES('retained')")
  await platform.restart()
  const reopened = await waitForRuntime(request, platform.url, created.project.id, 'running')
  expect(reopened.target?.resource).toEqual(running.target?.resource)
  expect(reopened.observation?.containerId).toBe(running.observation?.containerId)
  expect(reopened.observation?.serverUuid).toBe(running.observation?.serverUuid)
  expect(await witness(reopened, 'SELECT value FROM runtime_witness.marker')).toBe('retained')
  await post<RuntimeReceipt>(request, platform.url, path, { ...command, mutationId: randomUUID(), baseGeneration: reopened.generation, desired: 'stopped' }, 202, 'RuntimeReceipt')
  const stopped = await waitForRuntime(request, platform.url, created.project.id, 'stopped')
  expect(stopped.observation?.volumeFound).toBe(true)
  await platform.restart(); await delay(2500)
  const stillStopped = await read(request, platform.url, created.project.id)
  expect(stillStopped.target?.desired).toBe('stopped'); expect(stillStopped.observation?.running).toBe(false)
  await post<RuntimeReceipt>(request, platform.url, path, { ...command, mutationId: randomUUID(), baseGeneration: stillStopped.generation, desired: 'running' }, 202, 'RuntimeReceipt')
  const resumed = await waitForRuntime(request, platform.url, created.project.id, 'running')
  expect(resumed.target?.resource).toEqual(running.target?.resource)
  expect(resumed.observation?.serverUuid).toBe(running.observation?.serverUuid)
  expect(await witness(resumed, 'SELECT value FROM runtime_witness.marker')).toBe('retained')
})
