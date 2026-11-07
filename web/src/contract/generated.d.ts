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
 * 本地不可变ID、证据类型与Buildx实际导出摘要；独立配置或描述符仅在工具确实提供时保存，不补造缺字段。
 */
export type BuildImage = {
  /**
   * 实际Docker image inspect的不可变ID，必须匹配idKind对应的已检查内容摘要。
   */
  id: string
  manifestDigest?: string
  configDigest?: string
  tag: string
  binaryDigest: string
  idKind: 'config' | 'manifest' | 'index'
  /**
   * 本次Buildx实际containerimage.digest，不假定它恒为manifest。
   */
  exportDigest: string
  manifestMediaType?:
    | 'application/vnd.oci.image.manifest.v1+json'
    | 'application/vnd.docker.distribution.manifest.v2+json'
    | 'application/vnd.oci.image.index.v1+json'
    | 'application/vnd.docker.distribution.manifest.list.v2+json'
} & BuildImage1
export type BuildImage1 =
  | {
      idKind?: 'config'
      configDigest: string
    }
  | {
      idKind?: 'manifest'
      manifestDigest: string
      manifestMediaType:
        'application/vnd.oci.image.manifest.v1+json' | 'application/vnd.docker.distribution.manifest.v2+json'
    }
  | {
      idKind?: 'index'
      manifestDigest: string
      manifestMediaType:
        'application/vnd.oci.image.index.v1+json' | 'application/vnd.docker.distribution.manifest.list.v2+json'
    }
/**
 * 仅同库元数据，不声明外部Git/Docker仍可用；另经v3精确匹配核实。
 */
export type StateBuildSnapshot = BuildSnapshot & {
  candidateCurrent?: false
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
  buildCommand?: BuildCommand
  buildCancelCommand?: BuildCancelCommand
  buildStep?: BuildStep
  buildOperation?: BuildOperation
  buildReceipt?: BuildReceipt
  buildSnapshot?: BuildSnapshot
  buildCapability?: BuildCapability
  deployPreviewRequest?: DeployPreviewRequest
  deployCommand?: DeployCommand
  deployPlan?: DeployPlan
  deployOperation?: DeployOperation
  deployReceipt?: DeployReceipt
  deploySnapshot?: DeploySnapshot
  deployCapability?: DeployCapability
  stateSnapshot?: StateSnapshot
  stateEvent?: StateEvent
  stateCaughtUp?: StateCaughtUp
  stateResync?: StateResync
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
export interface BuildCommand {
  mutationId: string
  baseRevision: string
  baseGeneration: string
  commit: string
  profile: BuildIdentity
  retryOf?: string
}
export interface BuildIdentity {
  id: string
  version: string
  digest: string
}
export interface BuildCancelCommand {
  mutationId: string
  baseVersion: string
}
export interface BuildStep {
  name: string
  state: 'pending' | 'executing' | 'succeeded' | 'failed' | 'needs_attention' | 'cancelled'
  code: string
  updatedAt: string
}
export interface BuildOperation {
  id: string
  targetId: string
  projectId: string
  serviceId: string
  version: string
  state: 'accepted' | 'executing' | 'cancelling' | 'succeeded' | 'cancelled' | 'failed' | 'needs_attention'
  code: string
  message: string
  cancelRequested: boolean
  /**
   * @maxItems 32
   */
  steps: BuildStep[]
  createdAt: string
  updatedAt: string
  candidate: BuildCandidate | null
}
export interface BuildCandidate {
  id: string
  targetId: string
  operationId: string
  inputDigest: string
  sourceDigest: string
  profile: BuildIdentity
  template: BuildIdentity
  environment: BuildEnvironment
  /**
   * @maxItems 32
   */
  checks: BuildCheck[]
  /**
   * @maxItems 512
   */
  migrations: BuildMigration[]
  migrationDigest: string
  image: BuildImage
  createdAt: string
  source: BuildSource
  policy: BuildPolicy
}
export interface BuildEnvironment {
  context: string
  daemonId: string
  dockerVersion: string
  buildxVersion: string
  builderName: string
  buildkitVersion: string
  imageId: string
  imageReference: string
  architecture: 'amd64' | 'arm64'
  operatingSystem: 'linux'
  bindSourceMode: 'canonical' | 'desktop-host-mnt-v1'
  uid: number
  gid: number
  endpointDigest: string
}
export interface BuildCheck {
  name: string
  state: 'succeeded'
  toolVersion: string
  outputDigest: string
  completedAt: string
}
export interface BuildMigration {
  path: string
  digest: string
}
export interface BuildSource {
  projectId: string
  projectRevision: string
  serviceId: string
  repositoryId: string
  repositoryPath: string
  pinnedCommit: string
}
export interface BuildPolicy {
  id: 'go-container-local-v1'
  version: 1
  totalSeconds: 480
  commandSeconds: 180
  sourceBytes: 67108864
  sourceFiles: 4096
  binaryBytes: 134217728
  memoryMiB: 4096
  tmpfsMiB: 3072
  pids: 256
  cpus: 2
}
export interface BuildReceipt {
  schemaVersion: 1
  mutationId: string
  kind: 'build' | 'cancelBuild'
  operationId: string
  target: BuildTarget
  version: string
  replayed: boolean
}
export interface BuildTarget {
  id: string
  projectId: string
  serviceId: string
  generation: string
  canvasRevision: string
  installationId: string
  source: BuildSource
  profile: BuildIdentity
  environment: BuildEnvironment
  policy: BuildPolicy
  inputDigest: string
  createdAt: string
}
export interface BuildSnapshot {
  schemaVersion: 1
  projectId: string
  serviceId: string
  generation: string
  target: BuildTarget | null
  operation: BuildOperation | null
  candidate: BuildCandidate | null
  candidateCurrent: boolean
  candidateCode: string
  currentSource: SourceVersion
}
export interface BuildCapability {
  schemaVersion: 1
  enabled: boolean
  adapter: string
  code: string
  message: string
  profile: BuildIdentity
  imageReference: string
  policy: BuildPolicy
}
export interface DeployPreviewRequest {
  baseRevision: string
  baseGeneration: string
  candidateId: string
  bindingId: string
  hostPort: number
  configuration: {}
}
export interface DeployCommand {
  mutationId: string
  preview: DeployPreviewRequest
  planDigest: string
  retryOf?: string
}
export interface DeployPlan {
  schemaVersion: 1
  projectId: string
  serviceId: string
  serviceName: string
  request: DeployPreviewRequest
  contract: DefinitionRef
  binding: DatabaseBinding
  candidate: BuildCandidate
  database: DeployDatabase
  environment: DeployEnvironment
  policy: DeployPolicy
  resources: DeployResources
  runtimeDigest: string
  digest: string
}
export interface DeployDatabase {
  resourceId: string
  installationId: string
  daemonId: string
  componentId: string
  generation: string
  resourceDigest: string
  configurationHash: string
  serverUuid: string
  network: string
  networkCidr: string
  volume: string
  container: string
  image: string
}
export interface DeployEnvironment {
  context: string
  daemonId: string
  dockerVersion: string
  composeVersion: string
  architecture: 'amd64' | 'arm64'
  bindSourceMode: 'native' | 'desktop-host-mnt-v1'
  uid: number
  gid: number
  buildInstallationId: string
  runtimeInstallationId: string
}
export interface DeployPolicy {
  id: 'go-mysql-compose-v1'
  version: 1
  totalSeconds: 360
  migrationSeconds: 120
  readinessSeconds: 120
  commandSeconds: 45
  leaseSeconds: 420
  memoryMiB: 512
  tmpfsMiB: 64
  pids: 128
  cpus: 1
}
export interface DeployResources {
  composeProject: string
  application: string
  migration: string
  applicationUser: string
  ingressNetwork: string
  migrationUser: string
  accountHost: string
}
export interface DeployOperation {
  id: string
  targetId: string
  projectId: string
  serviceId: string
  version: string
  state: 'accepted' | 'executing' | 'succeeded' | 'failed' | 'needs_attention'
  code: string
  message: string
  /**
   * @maxItems 16
   */
  steps: DeployStep[]
  createdAt: string
  updatedAt: string
}
export interface DeployStep {
  name: 'preflight' | 'database' | 'material' | 'migration' | 'permissions' | 'container' | 'readiness'
  state: 'executing' | 'succeeded' | 'unknown' | 'failed'
  code: string
  updatedAt: string
}
export interface DeployReceipt {
  schemaVersion: 1
  mutationId: string
  operationId: string
  target: DeployTarget
  replayed: boolean
}
export interface DeployTarget {
  id: string
  projectId: string
  serviceId: string
  generation: string
  plan: DeployPlan
  createdAt: string
}
export interface DeploySnapshot {
  schemaVersion: 1
  projectId: string
  serviceId: string
  generation: string
  sequence: string
  target: DeployTarget | null
  operation: DeployOperation | null
  observation: DeployObservation | null
  freshness: 'fresh' | 'stale' | 'unknown'
  observationCode: string
  lastAttemptAt: string | ''
}
export interface DeployObservation {
  state: 'not_created' | 'stopped' | 'running' | 'ready' | 'drifted' | 'unknown'
  running: boolean
  healthy: boolean
  ready: boolean
  containerId: string
  imageId: string
  entryUrl: string
  databaseUuid: string
  migrationVersion: number
  migrationIntegrity: 'missing' | 'dirty' | 'verified' | 'mismatch' | 'unknown'
  observedAt: string
}
export interface DeployCapability {
  schemaVersion: 1
  enabled: boolean
  adapter: string
  code: string
  message: string
  policy: DeployPolicy
}
export interface StateSnapshot {
  schemaVersion: 1
  project: Project
  graph: GraphState
  runtime: RuntimeSnapshot
  /**
   * @maxItems 500
   */
  builds: StateBuildSnapshot[]
  /**
   * @maxItems 500
   */
  deployments: DeploySnapshot[]
  cursor: string
  replayFloor: string
  capturedAt: string
  policy: StatePolicy
}
export interface StatePolicy {
  heartbeatSeconds: 15
  idleSeconds: 45
  staleSeconds: 20
  retentionSeconds: 86400
  maxEvents: 100000
  pageSize: 200
  maxFrameBytes: 4096
  maxSnapshotBytes: 67108864
  maxConnections: 16
  maxProjectConnections: 4
  retrySeconds: 2
}
export interface StateEvent {
  schemaVersion: 1
  id: string
  projectId: string
  sequence: string
  scope: 'project' | 'runtime' | 'build' | 'deployment'
  revision: string
  reference: StateReference | null
  occurredAt: string
}
export interface StateReference {
  serviceId?: string
  generation: string
  operationId: string
  operationVersion: string
  execution: string
}
export interface StateCaughtUp {
  schemaVersion: 1
  projectId: string
  cursor: string
}
export interface StateResync {
  schemaVersion: 1
  projectId: string
  code: 'STATE_CURSOR_EXPIRED' | 'STATE_CURSOR_AHEAD' | 'STATE_EVENT_GAP'
}
