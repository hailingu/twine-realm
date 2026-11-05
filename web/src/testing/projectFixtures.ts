/** 测试夹具使用发布类型，不借助类型断言绕过契约。 */
import { vi } from 'vitest'
import type { CanvasReceipt, CanvasState, DefinitionCatalog, MiddlewareNode, Project, Receipt, SourceObservation, SourceReceipt } from '../contract/generated'
import type { ProjectAPI } from '../project/api'
import { parseContract } from '../project/api'
import definition from '../../../server/internal/catalog/definitions/mysql-v1.json'
import { CATALOG_DIGEST, MYSQL_DEFAULTS, MYSQL_REFERENCE } from '../contract/catalog.generated'

/** 可复用的稳定项目身份夹具。 */
export const INITIAL_PROJECT: Project = { schemaVersion: 1, id: '00000000-0000-4000-8000-000000000001', name: '本地项目', revision: '1', nodes: [{ id: '00000000-0000-4000-8000-000000000002', displayName: '本地服务', repository: { id: '00000000-0000-4000-8000-000000000003', path: '/allowed/service', headCommit: null }, position: { x: 100, y: 100 } }], createdAt: '2026-11-04T00:00:00Z', updatedAt: '2026-11-04T00:00:00Z' }

/** 当前工作区观察与保存选择互不代替。 */
export const INITIAL_SOURCE: SourceObservation = { version: { projectId: INITIAL_PROJECT.id, projectRevision: '1', serviceId: '00000000-0000-4000-8000-000000000002', repositoryId: '00000000-0000-4000-8000-000000000003', repositoryPath: '/allowed/service', pinnedCommit: null }, git: { available: true, observedAt: '2026-11-04T00:00:00Z', headCommit: 'a'.repeat(40), branch: 'develop', dirty: false, pinnedAvailable: null, warning: null, problem: null } }

/** @param mutationId 原请求身份 @param commit 明确选择 @returns 原子固定后的专用回执。 */
export function sourceReceipt(mutationId: string, commit = 'a'.repeat(40)): SourceReceipt { return { mutationId, kind: 'pinSource', replayed: false, project: { ...INITIAL_PROJECT, revision: '2' }, sourceVersion: { ...INITIAL_SOURCE.version, projectRevision: '2', pinnedCommit: commit } } }

/** @param project 确认项目 @param kind 用例类型 @param mutationId 原请求身份 @returns 完整回执。 */
export function receipt(project: Project, kind: Receipt['kind'], mutationId: string): Receipt { return { project, kind, mutationId, replayed: false } }

/** 空标准组件集合对应旧项目的增量画布状态。 */
export const EMPTY_CANVAS: CanvasState = { schemaVersion: 1, middlewares: [] }
const { id, version, ...definitionFields } = definition
/** 与生产内置包相同的真实目录，测试不手写第二份配置 schema。 */
export const INITIAL_CATALOG = parseContract<DefinitionCatalog>('DefinitionCatalog', { schemaVersion: 1, digest: CATALOG_DIGEST, definitions: [{ ...definitionFields, reference: { id, version, digest: CATALOG_DIGEST } }] })
/** 可编辑的稳定 MySQL 草稿夹具。 */
export const MYSQL_NODE: MiddlewareNode = { id: '00000000-0000-4000-8000-000000000040', kind: 'mysql', displayName: '业务数据库', definition: MYSQL_REFERENCE, configuration: MYSQL_DEFAULTS, position: { x: 320, y: 240 }, notReadyReasons: ['缺少运行必需配置：runtimeProfile'] }

/** @param project 原服务集合 @param kind 画布命令类型 @param mutationId 原请求身份 @param canvas 标准组件集合 @returns 完整画布专用回执。 */
export function canvasReceipt(project: Project, kind: CanvasReceipt['kind'], mutationId: string, canvas: CanvasState = EMPTY_CANVAS): CanvasReceipt { return { project, canvas, kind, mutationId, replayed: false } }

/** @returns 默认成功的应用端口，具体失败由行为用例注入。 */
export function fakeAPI() {
  return {
    list: vi.fn<ProjectAPI['list']>().mockResolvedValue({ projects: [] }),
    get: vi.fn<ProjectAPI['get']>().mockResolvedValue(INITIAL_PROJECT),
    create: vi.fn<ProjectAPI['create']>().mockImplementation(command => Promise.resolve(receipt(INITIAL_PROJECT, 'createProject', command.mutationId))),
    register: vi.fn<ProjectAPI['register']>().mockImplementation((_id, command) => Promise.resolve(receipt(INITIAL_PROJECT, 'registerRepository', command.mutationId))),
    layout: vi.fn<ProjectAPI['layout']>().mockImplementation((_id, command) => Promise.resolve(canvasReceipt({ ...INITIAL_PROJECT, revision: '2', nodes: INITIAL_PROJECT.nodes.map(node => { const position = command.positions.find(item => item.componentId === node.id); return { ...node, position: position ? { x: position.x, y: position.y } : node.position } }) }, 'saveCanvasLayout', command.mutationId))),
    receipt: vi.fn<ProjectAPI['receipt']>().mockRejectedValue(new Error('未提供测试回执')),
    source: vi.fn<ProjectAPI['source']>().mockResolvedValue(INITIAL_SOURCE),
    pin: vi.fn<ProjectAPI['pin']>().mockImplementation((_id, _serviceId, command) => Promise.resolve(sourceReceipt(command.mutationId, command.commit))),
    sourceReceipt: vi.fn<ProjectAPI['sourceReceipt']>().mockRejectedValue(new Error('未提供源码回执')),
    canvas: vi.fn<ProjectAPI['canvas']>().mockResolvedValue({ project: INITIAL_PROJECT, canvas: EMPTY_CANVAS }),
    catalog: vi.fn<ProjectAPI['catalog']>().mockResolvedValue(INITIAL_CATALOG),
    addMiddleware: vi.fn<ProjectAPI['addMiddleware']>().mockImplementation((_id, command) => Promise.resolve(canvasReceipt({ ...INITIAL_PROJECT, revision: '2' }, 'addMiddleware', command.mutationId, { schemaVersion: 1, middlewares: [{ ...MYSQL_NODE, displayName: command.displayName, configuration: { ...MYSQL_DEFAULTS, ...command.configuration }, position: command.position }] }))),
    updateMiddleware: vi.fn<ProjectAPI['updateMiddleware']>().mockImplementation((_id, componentId, command) => Promise.resolve(canvasReceipt({ ...INITIAL_PROJECT, revision: '2' }, 'updateMiddleware', command.mutationId, { schemaVersion: 1, middlewares: [{ ...MYSQL_NODE, id: componentId, displayName: command.displayName, configuration: { ...MYSQL_DEFAULTS, ...command.configuration }, position: command.position }] }))),
    canvasReceipt: vi.fn<ProjectAPI['canvasReceipt']>().mockRejectedValue(new Error('未提供画布回执'))
  }
}
