/** 外部信封、通知和候选环境核实必须通过交叉归属与版本检查。 */
import { expect, it } from 'vitest'
import { checkedBuild, ownedState, parseStateMessage, stateNumber } from './model'
import { stateEvent, stateSnapshot } from '../testing/stateFixtures'
import { INITIAL_PROJECT } from '../testing/projectFixtures'
import { READY_BUILD } from '../testing/buildFixtures'
import { READY_DEPLOYMENT } from '../testing/deploymentFixtures'
import { deploymentLabel } from './runtimeLabel'
import { READY_RUNTIME } from '../testing/runtimeFixtures'
import { BOUND_GRAPH } from '../testing/graphFixtures'

it('信封和通知使用完整字符串整数，拒绝未知协议、成员缺失和错误归属', () => {
  const value = stateSnapshot(); const id = INITIAL_PROJECT.id
  expect(ownedState(value, id)).toEqual(value)
  expect(stateNumber('9007199254740993')).toBe(9007199254740993n)
  for (const invalid of ['00', '-1', '1.0', '18446744073709551616']) expect(() => stateNumber(invalid)).toThrow()
  for (const invalid of [{ ...value, schemaVersion: 99 }, { ...value, cursor: '0', replayFloor: '1' }, { ...value, builds: [] }, { ...value, deployments: [...value.deployments, ...value.deployments] }, { ...value, builds: value.builds.map(build => ({ ...build, currentSource: { ...build.currentSource, projectRevision: '9' } })) }]) expect(() => ownedState(invalid, id)).toThrow()
  expect(() => ownedState(value, '00000000-0000-4000-8000-000000000099')).toThrow()
  expect(parseStateMessage('state', id, '2', stateEvent('2'))).toMatchObject({ kind: 'state', value: { sequence: '2' } })
  expect(parseStateMessage('caught_up', id, null, { schemaVersion: 1, projectId: id, cursor: '2' }).kind).toBe('caughtUp')
  expect(parseStateMessage('resync', id, null, { schemaVersion: 1, projectId: id, code: 'STATE_EVENT_GAP' }).kind).toBe('resync')
  for (const [event, wireId, data] of [['unknown', null, {}], ['state', '3', stateEvent('2')], ['state', '2', { ...stateEvent('2'), scope: 'runtime' }], ['caught_up', '2', { schemaVersion: 1, projectId: id, cursor: '2' }]] as const) expect(() => parseStateMessage(event, id, wireId, data)).toThrow()
})

it('候选环境核实只附着到完全匹配的当前元数据，旧操作不能授权新目标', () => {
  const metadata = { ...READY_BUILD, candidateCurrent: false, candidateCode: 'HISTORICAL_CANDIDATE' }
  expect(checkedBuild(metadata, READY_BUILD).candidateCurrent).toBe(true)
  expect(checkedBuild(metadata, { ...READY_BUILD, generation: '2' })).toMatchObject({ candidateCurrent: false, candidateCode: 'CURRENT_STATE_UNCONFIRMED' })
  expect(checkedBuild(metadata, { ...READY_BUILD, currentSource: { ...READY_BUILD.currentSource, pinnedCommit: 'b'.repeat(40) } }).candidateCurrent).toBe(false)
})

it('完整信封拒绝属于其他项目或代次的资源/目标/操作，而不是只校验顶层项目', () => {
  const id = INITIAL_PROJECT.id; const foreign = '00000000-0000-4000-8000-000000000099'
  const target = READY_RUNTIME.target; const operation = READY_RUNTIME.operation; const buildTarget = READY_BUILD.target; const buildOperation = READY_BUILD.operation
  if (!target || !operation || !buildTarget || !buildOperation) throw new Error('缺少完整目标夹具')
  const value = { ...stateSnapshot(), graph: BOUND_GRAPH, runtime: READY_RUNTIME, builds: [{ ...READY_BUILD, candidateCurrent: false, candidateCode: 'HISTORICAL_CANDIDATE' }], deployments: [READY_DEPLOYMENT] }
  expect(ownedState(value, id).runtime.generation).toBe('1')
  const invalid = [
    { ...value, builds: [{ ...READY_BUILD, candidateCurrent: true }] },
    { ...value, runtime: { ...READY_RUNTIME, target: { ...target, resource: { ...target.resource, projectId: foreign } } } },
    { ...value, runtime: { ...READY_RUNTIME, operation: { ...operation, generation: '2' } } },
    { ...value, builds: [{ ...value.builds[0], target: { ...buildTarget, projectId: foreign } }] },
    { ...value, builds: [{ ...value.builds[0], operation: { ...buildOperation, serviceId: foreign } }] }
  ]
  for (const input of invalid) expect(() => ownedState(input, id)).toThrow()
})

it('业务运行、健康、就绪、漂移和观测过期互不混淆', () => {
  const now = Date.parse(READY_DEPLOYMENT.lastAttemptAt)
  expect(deploymentLabel(READY_DEPLOYMENT, '', now).tone).toBe('ready')
  expect(deploymentLabel(READY_DEPLOYMENT, '', now + 21000).text).toContain('过期')
  expect(deploymentLabel(READY_DEPLOYMENT, '读取失败', now).tone).toBe('unknown')
  expect(deploymentLabel(undefined, '', now).text).toContain('尚未部署')
  const fact = READY_DEPLOYMENT.observation; if (!fact) throw new Error('缺少夹具事实')
  expect(deploymentLabel({ ...READY_DEPLOYMENT, observation: { ...fact, state: 'drifted', ready: false } }, '', now).tone).toBe('error')
  expect(deploymentLabel({ ...READY_DEPLOYMENT, observation: { ...fact, ready: false, healthy: true } }, '', now).text).toContain('尚未就绪')
  expect(deploymentLabel({ ...READY_DEPLOYMENT, observation: { ...fact, ready: false, healthy: false } }, '', now).text).toContain('健康未确认')
  expect(deploymentLabel({ ...READY_DEPLOYMENT, observation: { ...fact, state: 'stopped', running: false, ready: false } }, '', now).text).toContain('已停止')
})
