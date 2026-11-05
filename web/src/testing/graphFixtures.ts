/** 图用例复用共享服务契约材料；固定摘要只模拟已通过 HTTP 边界的确认回执。 */
import type { DatabaseBinding, GraphState, ServiceContractDocument } from '../contract/generated'
import { SERVICE_CONTRACT_EXAMPLE } from '../contract/graph.generated'
import { parseContract } from '../project/api'
import { INITIAL_PROJECT, MYSQL_NODE } from './projectFixtures'

/** 每个测试取得独立候选，避免修改生成的只读示例。 */
export function serviceDocument(): ServiceContractDocument { return parseContract<ServiceContractDocument>('ServiceContractDocument', JSON.parse(JSON.stringify(SERVICE_CONTRACT_EXAMPLE))) }
/** 测试服务与原仓库身份保持一致。 */
export const GRAPH_SERVICE_ID = '00000000-0000-4000-8000-000000000002'
/** 显式声明后的具名消费 / 提供端口集合。 */
export const DECLARED_GRAPH: GraphState = { schemaVersion: 2, middlewares: [MYSQL_NODE], serviceContracts: [{ serviceId: GRAPH_SERVICE_ID, reference: { id: SERVICE_CONTRACT_EXAMPLE.id, version: SERVICE_CONTRACT_EXAMPLE.version, digest: 'sha256:' + '1'.repeat(64) }, document: serviceDocument() }], bindings: [] }
/** 已确认关系携带稳定数据库身份和凭据引用，不携带任何密码。 */
export const DB_BINDING: DatabaseBinding = { id: '00000000-0000-4000-8000-000000000050', kind: 'databaseDependency', source: { componentId: GRAPH_SERVICE_ID, portId: 'database' }, target: { componentId: MYSQL_NODE.id, portId: 'mysql' }, sourceDefinition: { id: SERVICE_CONTRACT_EXAMPLE.id, version: SERVICE_CONTRACT_EXAMPLE.version, digest: 'sha256:' + '1'.repeat(64) }, targetDefinition: MYSQL_NODE.definition, protocol: 'mysql', contract: 'mysql-wire/10', databaseId: '00000000-0000-4000-8000-000000000051', databaseName: 'app_local', purpose: 'readWrite', credential: { id: '00000000-0000-4000-8000-000000000052', version: '1' } }
/** 已保存图用于恢复、重连和兼容响应的行为验证。 */
export const BOUND_GRAPH: GraphState = { ...DECLARED_GRAPH, bindings: [DB_BINDING] }
/** @returns 与图夹具一致的版本，调用者可明确推进修订。 */
export function graphProject(revision = '2') { return { ...INITIAL_PROJECT, revision } }
