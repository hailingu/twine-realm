/** 测试夹具使用发布类型，不借助类型断言绕过契约。 */
import { vi } from 'vitest'
import type { Project, Receipt, SourceObservation, SourceReceipt } from '../contract/generated'
import type { ProjectAPI } from '../project/api'

/** 可复用的稳定项目身份夹具。 */
export const INITIAL_PROJECT: Project = { schemaVersion: 1, id: '00000000-0000-4000-8000-000000000001', name: '本地项目', revision: '1', nodes: [{ id: '00000000-0000-4000-8000-000000000002', displayName: '本地服务', repository: { id: '00000000-0000-4000-8000-000000000003', path: '/allowed/service', headCommit: null }, position: { x: 100, y: 100 } }], createdAt: '2026-11-04T00:00:00Z', updatedAt: '2026-11-04T00:00:00Z' }

/** 当前工作区观察与保存选择互不代替。 */
export const INITIAL_SOURCE: SourceObservation = { version: { projectId: INITIAL_PROJECT.id, projectRevision: '1', serviceId: '00000000-0000-4000-8000-000000000002', repositoryId: '00000000-0000-4000-8000-000000000003', repositoryPath: '/allowed/service', pinnedCommit: null }, git: { available: true, observedAt: '2026-11-04T00:00:00Z', headCommit: 'a'.repeat(40), branch: 'develop', dirty: false, pinnedAvailable: null, warning: null, problem: null } }

/** @param mutationId 原请求身份 @param commit 明确选择 @returns 原子固定后的专用回执。 */
export function sourceReceipt(mutationId: string, commit = 'a'.repeat(40)): SourceReceipt { return { mutationId, kind: 'pinSource', replayed: false, project: { ...INITIAL_PROJECT, revision: '2' }, sourceVersion: { ...INITIAL_SOURCE.version, projectRevision: '2', pinnedCommit: commit } } }

/** @param project 确认项目 @param kind 用例类型 @param mutationId 原请求身份 @returns 完整回执。 */
export function receipt(project: Project, kind: Receipt['kind'], mutationId: string): Receipt { return { project, kind, mutationId, replayed: false } }

/** @returns 默认成功的应用端口，具体失败由行为用例注入。 */
export function fakeAPI() {
  return {
    list: vi.fn<ProjectAPI['list']>().mockResolvedValue({ projects: [] }),
    get: vi.fn<ProjectAPI['get']>().mockResolvedValue(INITIAL_PROJECT),
    create: vi.fn<ProjectAPI['create']>().mockImplementation(command => Promise.resolve(receipt(INITIAL_PROJECT, 'createProject', command.mutationId))),
    register: vi.fn<ProjectAPI['register']>().mockImplementation((_id, command) => Promise.resolve(receipt(INITIAL_PROJECT, 'registerRepository', command.mutationId))),
    layout: vi.fn<ProjectAPI['layout']>().mockImplementation((_id, command) => Promise.resolve(receipt({ ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => ({ ...node, position: command.positions.find(item => item.serviceId === node.id) ?? node.position })) }, 'saveLayout', command.mutationId))),
    receipt: vi.fn<ProjectAPI['receipt']>().mockRejectedValue(new Error('未提供测试回执')),
    source: vi.fn<ProjectAPI['source']>().mockResolvedValue(INITIAL_SOURCE),
    pin: vi.fn<ProjectAPI['pin']>().mockImplementation((_id, _serviceId, command) => Promise.resolve(sourceReceipt(command.mutationId, command.commit))),
    sourceReceipt: vi.fn<ProjectAPI['sourceReceipt']>().mockRejectedValue(new Error('未提供源码回执'))
  }
}
