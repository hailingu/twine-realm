/** 由 server/contract/v1/schema.json 生成，请运行 npm run contract；禁止手工修改。 */

/**
 * 一期本地项目登记的唯一机器契约；非标准服务不由本契约假定业务技术栈
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
