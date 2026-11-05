/** 由 server/contract/v1/schema.json 生成，请运行 npm run contract；禁止手工修改。 */

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
