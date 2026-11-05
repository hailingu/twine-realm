/** 由 server/contract/ 的离线版本化 schema 生成，请运行 npm run contract；禁止手工修改。 */

/**
 * 版本二服务契约与数据库依赖声明
 */
export type GraphOperation =
  | {
      type: 'service.setContract'
      serviceId: string
      document: ServiceContractDocument
    }
  | {
      type: 'binding.create'
      source: PortEndpoint
      target: PortEndpoint
      databaseName: string
      purpose: 'readWrite' | 'readOnly'
    }
  | {
      type: 'binding.reconnect'
      bindingId: string
      source: PortEndpoint
      target: PortEndpoint
      databaseName: string
      purpose: 'readWrite' | 'readOnly'
    }
  | {
      type: 'binding.remove'
      bindingId: string
    }

/**
 * 本地项目登记与增量源码追溯的权威契约；原 Project/Repository/Receipt 保持不变
 */
export interface Contract {
  position?: Position
  repository?: Repository
  serviceNode?: ServiceNode
  project?: Project
  summary?: Summary
  projectList?: ProjectList
  createCommand?: CreateCommand
  registerCommand?: RegisterCommand
  nodePosition?: NodePosition
  layoutCommand?: LayoutCommand
  receipt?: Receipt
  aPIError?: APIError
  errorResponse?: ErrorResponse
  sourceVersion?: SourceVersion
  gitObservation?: GitObservation
  sourceObservation?: SourceObservation
  pinSourceCommand?: PinSourceCommand
  sourceReceipt?: SourceReceipt
  definitionRef?: DefinitionRef
  portDefinition?: PortDefinition
  nodeDefinition?: NodeDefinition
  definitionCatalog?: DefinitionCatalog
  middlewareNode?: MiddlewareNode
  canvasState?: CanvasState
  canvasSnapshot?: CanvasSnapshot
  middlewareCommand?: MiddlewareCommand
  canvasPosition?: CanvasPosition
  canvasLayoutCommand?: CanvasLayoutCommand
  canvasReceipt?: CanvasReceipt
  runtimeConfiguration?: RuntimeConfiguration
  runtimeCommand?: RuntimeCommand
  runtimeResource?: RuntimeResource
  runtimeTarget?: RuntimeTarget
  runtimeStep?: RuntimeStep
  runtimeOperation?: RuntimeOperation
  runtimeReceipt?: RuntimeReceipt
  runtimeObservation?: RuntimeObservation
  runtimeSnapshot?: RuntimeSnapshot
  runtimeCapability?: RuntimeCapability
  runtimeEvent?: RuntimeEvent
  runtimeEventPage?: RuntimeEventPage
  serviceInterface?: ServiceInterface
  servicePort?: ServicePort
  serviceContractDocument?: ServiceContractDocument
  serviceContract?: ServiceContract
  portEndpoint?: PortEndpoint
  credentialReference?: CredentialReference
  databaseBinding?: DatabaseBinding
  graphState?: GraphState
  graphSnapshot?: GraphSnapshot
  graphOperation?: GraphOperation
  graphEditCommand?: GraphEditCommand
  graphReceipt?: GraphReceipt
}
/**
 * 版本一Position契约
 */
export interface Position {
  x: number
  y: number
}
/**
 * 版本一Repository契约
 */
export interface Repository {
  id: string
  path: string
  /**
   * 登记时观测的源提交；空仓库为 null，不代表构建就绪
   */
  headCommit: string | null
}
/**
 * 版本一ServiceNode契约
 */
export interface ServiceNode {
  id: string
  displayName: string
  repository: Repository
  position: Position
}
/**
 * 已提交项目快照，节点位置不是运行状态
 */
export interface Project {
  schemaVersion: 1
  id: string
  name: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  revision: string
  /**
   * @maxItems 500
   */
  nodes: ServiceNode[]
  createdAt: string
  updatedAt: string
}
/**
 * 版本一Summary契约
 */
export interface Summary {
  id: string
  name: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  revision: string
  nodeCount: number
  updatedAt: string
}
/**
 * 版本一ProjectList契约
 */
export interface ProjectList {
  projects: Summary[]
}
/**
 * 版本一CreateCommand契约
 */
export interface CreateCommand {
  mutationId: string
  name: string
}
/**
 * 版本一RegisterCommand契约
 */
export interface RegisterCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  repositoryPath: string
  displayName?: string
  position: Position
}
/**
 * 版本一NodePosition契约
 */
export interface NodePosition {
  serviceId: string
  x: number
  y: number
}
/**
 * 版本一LayoutCommand契约
 */
export interface LayoutCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  /**
   * @maxItems 500
   */
  positions: NodePosition[]
}
/**
 * 同一事务持久化的请求结果，重放不重复执行
 */
export interface Receipt {
  mutationId: string
  kind: 'createProject' | 'registerRepository' | 'saveLayout'
  project: Project
  replayed: boolean
}
/**
 * 版本一APIError契约
 */
export interface APIError {
  code: string
  message: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  currentRevision?: string
  existingServiceId?: string
}
/**
 * 版本一ErrorResponse契约
 */
export interface ErrorResponse {
  error: APIError
}
/**
 * 版本一新增源码追溯契约
 */
export interface SourceVersion {
  projectId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  projectRevision: string
  serviceId: string
  repositoryId: string
  repositoryPath: string
  pinnedCommit: string | null
}
/**
 * 版本一新增源码追溯契约
 */
export interface GitObservation {
  available: boolean
  observedAt: string
  headCommit: string | null
  branch: string | null
  dirty: boolean | null
  pinnedAvailable: boolean | null
  warning: string | null
  problem: APIError | null
}
/**
 * 版本一新增源码追溯契约
 */
export interface SourceObservation {
  version: SourceVersion
  git: GitObservation
}
/**
 * 版本一新增源码追溯契约
 */
export interface PinSourceCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  commit: string
}
/**
 * 版本一新增源码追溯契约
 */
export interface SourceReceipt {
  mutationId: string
  kind: 'pinSource'
  project: Project
  replayed: boolean
  sourceVersion: SourceVersion
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface DefinitionRef {
  id: string
  version: string
  digest: string
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface PortDefinition {
  id: string
  role: 'provide' | 'consume'
  protocol: string
  contract: string
  cardinality: 'one' | 'many'
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface NodeDefinition {
  reference: DefinitionRef
  kind: 'mysql'
  title: string
  configurationSchema: {}
  /**
   * @maxItems 20
   */
  ports: PortDefinition[]
  image: string
  adapter: string
  effect: 'replace'
  /**
   * @maxItems 20
   */
  requiredForDeployment: string[]
  /**
   * @maxItems 20
   */
  requiredCapabilities: string[]
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface DefinitionCatalog {
  schemaVersion: 1
  digest: string
  /**
   * @maxItems 20
   */
  definitions: NodeDefinition[]
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface MiddlewareNode {
  id: string
  kind: 'mysql'
  displayName: string
  definition: DefinitionRef
  /**
   * 字段和值由固定定义版本的离线配置 schema 进一步校验；此容器不接受任意 schema 引用。
   */
  configuration: {
    [k: string]: unknown
  }
  position: Position
  /**
   * @maxItems 20
   */
  notReadyReasons: string[]
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface CanvasState {
  schemaVersion: 1
  /**
   * @maxItems 500
   */
  middlewares: MiddlewareNode[]
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface CanvasSnapshot {
  project: Project
  canvas: CanvasState
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface MiddlewareCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  definition: DefinitionRef
  displayName: string
  /**
   * 字段和值由固定定义版本的离线配置 schema 进一步校验；此容器不接受任意 schema 引用。
   */
  configuration: {
    [k: string]: unknown
  }
  position: Position
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface CanvasPosition {
  componentId: string
  x: number
  y: number
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface CanvasLayoutCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  /**
   * @maxItems 500
   */
  positions: CanvasPosition[]
}
/**
 * 版本一增量标准组件与完整画布契约
 */
export interface CanvasReceipt {
  mutationId: string
  kind: 'addMiddleware' | 'updateMiddleware' | 'saveCanvasLayout'
  project: Project
  replayed: boolean
  canvas: CanvasState
}
export interface RuntimeConfiguration {
  charset: 'utf8mb4'
  collation: 'utf8mb4_0900_ai_ci'
  memoryMiB: number
  runtimeProfile: 'local-compose-v1'
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeCommand {
  mutationId: string
  baseRevision: string
  baseGeneration: string
  desired: 'running' | 'stopped'
  retryOf?: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeResource {
  id: string
  installationId: string
  daemonId: string
  projectId: string
  componentId: string
  definition: DefinitionRef
  configuration: RuntimeConfiguration
  configurationHash: string
  image: 'mysql:9.7.4@sha256:df90258da46f6e7ff8574ff84f17618aba0765cac749928299c69fd4e7cb8ad8'
  network: string
  volume: string
  container: string
  secretRef: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeTarget {
  id: string
  projectId: string
  generation: string
  desired: 'running' | 'stopped'
  resource: RuntimeResource
  createdAt: string
  canvasRevision: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeStep {
  name: 'preflight' | 'credentials' | 'network' | 'volume' | 'container' | 'readiness' | 'stop' | 'stopped'
  state: 'pending' | 'executing' | 'unknown' | 'succeeded' | 'failed' | 'skipped'
  code: string
  externalId: string
  updatedAt: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeOperation {
  mutationId: string
  id: string
  projectId: string
  generation: string
  state: 'accepted' | 'executing' | 'waiting_retry' | 'needs_attention' | 'succeeded' | 'failed' | 'superseded'
  version: string
  attempts: number
  code: string
  message: string
  retryOf: '' | string
  createdAt: string
  updatedAt: string
  /**
   * @minItems 3
   * @maxItems 6
   */
  steps: [RuntimeStep, RuntimeStep, RuntimeStep, ...RuntimeStep[]]
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeReceipt {
  schemaVersion: 1
  mutationId: string
  operationId: string
  target: RuntimeTarget
  replayed: boolean
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeObservation {
  state: 'not_created' | 'running' | 'stopped' | 'drifted'
  running: boolean
  ready: boolean
  serverUuid: '' | string
  containerId: string
  volumeFound: boolean
  observedAt: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeSnapshot {
  schemaVersion: 1
  projectId: string
  generation: string
  sequence: string
  target: RuntimeTarget | null
  operation: RuntimeOperation | null
  observation: RuntimeObservation | null
  freshness: 'fresh' | 'stale' | 'unknown'
  observationCode: string
  lastAttemptAt: '' | string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeCapability {
  schemaVersion: 1
  enabled: boolean
  adapter: '' | 'file-secrets-v1'
  code: string
  message: string
  image: 'mysql:9.7.4@sha256:df90258da46f6e7ff8574ff84f17618aba0765cac749928299c69fd4e7cb8ad8'
  composeVersion: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeEvent {
  sequence: string
  operationId: string
  generation: string
  kind: string
  code: string
  createdAt: string
}
/**
 * 本机持久MySQL运行v1契约，不含秘密值
 */
export interface RuntimeEventPage {
  schemaVersion: 1
  /**
   * @maxItems 100
   */
  events: RuntimeEvent[]
  nextSequence: string
  hasMore: boolean
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface ServiceInterface {
  id: string
  version: string
  protocol: string
  /**
   * 有界 JSON Schema 2020-12；引用、编译与包大小由服务端进一步校验，不在浏览器动态编译。
   */
  schema: {
    [k: string]: unknown
  }
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface ServicePort {
  id: string
  role: 'consume' | 'provide'
  protocol: string
  contract: string
  cardinality: 'one' | 'many'
  required: boolean
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface ServiceContractDocument {
  id: string
  version: string
  /**
   * 有界 JSON Schema 2020-12；引用、编译与包大小由服务端进一步校验，不在浏览器动态编译。
   */
  configurationSchema: {
    [k: string]: unknown
  }
  /**
   * @maxItems 20
   */
  interfaces: ServiceInterface[]
  /**
   * @maxItems 20
   */
  ports: ServicePort[]
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface ServiceContract {
  serviceId: string
  reference: DefinitionRef
  document: ServiceContractDocument
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface PortEndpoint {
  componentId: string
  portId: string
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface CredentialReference {
  id: string
  version: string
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface DatabaseBinding {
  id: string
  kind: 'databaseDependency'
  source: PortEndpoint
  target: PortEndpoint
  sourceDefinition: DefinitionRef
  targetDefinition: DefinitionRef
  protocol: 'mysql'
  contract: 'mysql-wire/10'
  databaseId: string
  databaseName: string
  purpose: 'readWrite' | 'readOnly'
  credential: CredentialReference
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface GraphState {
  schemaVersion: 2
  /**
   * @maxItems 500
   */
  middlewares: MiddlewareNode[]
  /**
   * @maxItems 500
   */
  serviceContracts: ServiceContract[]
  /**
   * @maxItems 1000
   */
  bindings: DatabaseBinding[]
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface GraphSnapshot {
  project: Project
  graph: GraphState
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface GraphEditCommand {
  mutationId: string
  /**
   * 无符号聚合版本，以十进制字符串传递，避免浏览器精度损失
   */
  baseRevision: string
  /**
   * @minItems 1
   * @maxItems 50
   */
  operations: [GraphOperation, ...GraphOperation[]]
}
/**
 * 版本二服务契约与数据库依赖声明
 */
export interface GraphReceipt {
  mutationId: string
  kind: 'editGraph'
  project: Project
  replayed: boolean
  graph: GraphState
}
