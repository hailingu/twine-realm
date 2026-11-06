/** 真实客户端执行生成schema与跨字段归属检查，不把模拟端口当作实际部署证据。 */
import { afterEach, expect, it, vi } from 'vitest'
import { DEPLOY_CAPABILITY, DEPLOY_COMMAND, DEPLOY_PLAN, DEPLOY_RECEIPT, EMPTY_DEPLOYMENT, READY_DEPLOYMENT } from '../testing/deploymentFixtures'
import { parseContract, RequestError } from '../project/api'
import { DEPLOYMENT_API, ownedPlan, ownedReceipt, ownedSnapshot } from './api'
import { newerSnapshot, terminalDeployment } from './model'

afterEach(() => { vi.unstubAllGlobals() })

it('真实生成校验器接受生产者形状，拒绝密码、非法角色账号和任意附加配置', () => {
  expect(parseContract('DeployPlan', DEPLOY_PLAN)).toEqual(DEPLOY_PLAN)
  expect(parseContract('DeployCapability', { ...DEPLOY_CAPABILITY, enabled: false, adapter: 'disabled' })).toBeDefined()
  expect(parseContract('DeployReceipt', DEPLOY_RECEIPT)).toEqual(DEPLOY_RECEIPT)
  expect(parseContract('DeploySnapshot', READY_DEPLOYMENT)).toEqual(READY_DEPLOYMENT)
  for (const value of [{ ...DEPLOY_PLAN, password: 'synthetic-private' }, { ...DEPLOY_PLAN, resources: { ...DEPLOY_PLAN.resources, applicationUser: 'root' } }, { ...DEPLOY_PLAN, request: { ...DEPLOY_PLAN.request, configuration: { custom: true } } }]) expect(() => parseContract('DeployPlan', value)).toThrow(RequestError)
})

it('跨服务、换端口、过时代次和假HTTP就绪不能进入状态', () => {
  const { projectId, serviceId } = DEPLOY_PLAN
  expect(ownedPlan(DEPLOY_PLAN, projectId, serviceId, DEPLOY_COMMAND.preview)).toBe(DEPLOY_PLAN)
  expect(ownedReceipt(DEPLOY_RECEIPT, projectId, serviceId, DEPLOY_COMMAND)).toBe(DEPLOY_RECEIPT)
  expect(ownedSnapshot(EMPTY_DEPLOYMENT, projectId, serviceId)).toBe(EMPTY_DEPLOYMENT)
  expect(() => ownedPlan({ ...DEPLOY_PLAN, serviceId: projectId }, projectId, serviceId, DEPLOY_COMMAND.preview)).toThrow(RequestError)
  expect(() => ownedPlan(DEPLOY_PLAN, projectId, serviceId, { ...DEPLOY_COMMAND.preview, hostPort: 8082 })).toThrow(RequestError)
  expect(() => ownedReceipt({ ...DEPLOY_RECEIPT, target: { ...DEPLOY_RECEIPT.target, generation: '7' } }, projectId, serviceId, DEPLOY_COMMAND)).toThrow(RequestError)
  expect(() => ownedReceipt({ ...DEPLOY_RECEIPT, mutationId: projectId }, projectId, serviceId, DEPLOY_COMMAND)).toThrow(RequestError)
  expect(() => ownedSnapshot({ ...EMPTY_DEPLOYMENT, generation: '1' }, projectId, serviceId)).toThrow(RequestError)
  expect(() => ownedSnapshot({ ...READY_DEPLOYMENT, operation: null }, projectId, serviceId)).toThrow(RequestError)
  const observation = READY_DEPLOYMENT.observation!
  for (const changed of [{ ready: true, healthy: false }, { databaseUuid: projectId }, { entryUrl: 'http://0.0.0.0:8081' }, { imageId: DEPLOY_PLAN.digest }, { migrationIntegrity: 'dirty' as const }]) expect(() => ownedSnapshot({ ...READY_DEPLOYMENT, observation: { ...observation, ...changed } }, projectId, serviceId)).toThrow(RequestError)
})

it('晚响应不回退目标、操作、事件或观测时间，新代次不被旧结果覆盖', () => {
  const current = READY_DEPLOYMENT
  expect(newerSnapshot(null, current)).toBe(current)
  expect(newerSnapshot(current, { ...current, projectId: current.serviceId })).toBe(current)
  expect(newerSnapshot(current, EMPTY_DEPLOYMENT)).toBe(current)
  expect(newerSnapshot(current, { ...current, sequence: '19' })).toBe(current)
  expect(newerSnapshot(current, { ...current, operation: { ...current.operation!, version: '19' } })).toBe(current)
  expect(newerSnapshot(current, { ...current, lastAttemptAt: '2026-11-06T09:00:00Z' })).toBe(current)
  const next = { ...current, generation: '2' }
  expect(newerSnapshot(current, next)).toBe(next)
  expect(newerSnapshot(current, { ...current, sequence: '21' })).not.toBe(current)
  expect(terminalDeployment('accepted')).toBe(false)
  expect(terminalDeployment('needs_attention')).toBe(true)
})

it('同源v4客户端全部使用契约并核实原请求，响应对象每次独立读取', async () => {
  const fetcher = vi.fn<typeof fetch>().mockImplementation((input, options) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const value = url.endsWith('/capabilities') ? DEPLOY_CAPABILITY : url.endsWith('/preview') ? DEPLOY_PLAN : url.endsWith('/deployments') || url.includes('/deployment-mutations/') ? DEPLOY_RECEIPT : READY_DEPLOYMENT
    expect(url.startsWith('/api/v4/')).toBe(true)
    if (options?.method === 'POST') expect(options.headers).toMatchObject({ 'X-Twine-Request': '1' })
    return Promise.resolve(new Response(JSON.stringify(value)))
  })
  vi.stubGlobal('fetch', fetcher)
  const { projectId, serviceId } = DEPLOY_PLAN
  expect(await DEPLOYMENT_API.capability()).toEqual(DEPLOY_CAPABILITY)
  expect(await DEPLOYMENT_API.snapshot(projectId, serviceId)).toEqual(READY_DEPLOYMENT)
  expect(await DEPLOYMENT_API.preview(projectId, serviceId, DEPLOY_COMMAND.preview)).toEqual(DEPLOY_PLAN)
  expect(await DEPLOYMENT_API.accept(projectId, serviceId, DEPLOY_COMMAND)).toEqual(DEPLOY_RECEIPT)
  expect(await DEPLOYMENT_API.receipt(DEPLOY_COMMAND.mutationId)).toEqual(DEPLOY_RECEIPT)
  await expect(DEPLOYMENT_API.receipt(projectId)).rejects.toThrow(RequestError)
})
