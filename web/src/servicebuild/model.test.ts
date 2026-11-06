/** 构建版本与原请求分别受保护，旧会话完成不会把当前输入倒退。 */
import { expect, it } from 'vitest'
import { BUILD_COMMAND, BUILD_RECEIPT, EMPTY_BUILD, RUNNING_BUILD } from '../testing/buildFixtures'
import { buildReducer, initialBuild, terminalBuild } from './model'

it('投影按代次、操作版本与源码revision前进，布局和其他会话仍由工作区拥有', () => {
  const state = buildReducer(initialBuild(), { type: 'snapshot', value: RUNNING_BUILD })
  expect(buildReducer(state, { type: 'snapshot', value: EMPTY_BUILD })).toBe(state)
  expect(buildReducer(state, { type: 'snapshot', value: { ...RUNNING_BUILD, operation: { ...RUNNING_BUILD.operation!, version: '1' } } })).toBe(state)
  expect(buildReducer(state, { type: 'snapshot', value: { ...RUNNING_BUILD, currentSource: { ...RUNNING_BUILD.currentSource, projectRevision: '0' } } })).toBe(state)
  expect(buildReducer(state, { type: 'snapshot', value: { ...RUNNING_BUILD, currentSource: { ...RUNNING_BUILD.currentSource, projectRevision: '2' } } }).snapshot?.currentSource.projectRevision).toBe('2')
  expect(terminalBuild('needs_attention')).toBe(true); expect(terminalBuild('executing')).toBe(false)
})

it('同键未知只能原键重试，迟到完成和其他键不能解除保护', () => {
  const intent = { kind: 'build' as const, command: BUILD_COMMAND }
  const sending = buildReducer(initialBuild(), { type: 'sending', intent })
  expect(buildReducer(sending, { type: 'accepted', id: BUILD_RECEIPT.operationId, receipt: BUILD_RECEIPT })).toBe(sending)
  const unknown = buildReducer(sending, { type: 'unknown', id: BUILD_COMMAND.mutationId, message: '丢失响应' })
  expect(buildReducer(unknown, { type: 'sending', intent: { ...intent, command: { ...BUILD_COMMAND, mutationId: BUILD_RECEIPT.operationId } } })).toBe(unknown)
  const retry = buildReducer(unknown, { type: 'sending', intent })
  expect(buildReducer(retry, { type: 'accepted', id: BUILD_COMMAND.mutationId, receipt: BUILD_RECEIPT }).submission.kind).toBe('idle')
  expect(buildReducer(retry, { type: 'rejected', id: BUILD_COMMAND.mutationId, message: '冲突' }).submission.kind).toBe('rejected')
  expect(buildReducer(initialBuild(), { type: 'readFailed', message: '断线' }).readError).toBe('断线')
})
