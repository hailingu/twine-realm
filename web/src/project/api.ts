/** 在 HTTP 边界验证服务端响应，未验证的数据不能进入画布模型。 */
import validators from '../contract/validators.cjs'
import type { APIError, CreateCommand, ErrorResponse, LayoutCommand, Project, ProjectList, Receipt, RegisterCommand } from '../contract/generated'

/** @param name 受控的生成定义名 @param input 未知响应 @returns 按生成 schema 验证后的类型谓词。 */
function matches<T>(name: string, input: unknown): input is T {
  const validate = validators[name]
  if (!validate) throw new Error(`未注册的契约 ${name}`)
  return validate(input)
}

/** 结构化错误区分确定拒绝与无法确认的提交结果。 */
export class RequestError extends Error {
  /** @param failure 安全文案与机器代码 @param status HTTP 状态；0 表示传输或契约失败。 */
  constructor(readonly failure: APIError, readonly status: number, options?: ErrorOptions) { super(failure.message, options); this.name = 'RequestError' }
}

/** 读取端口与显式变更端口是工作台唯一的服务端副作用入口。 */
export interface ProjectAPI {
  list(signal?: AbortSignal): Promise<ProjectList>
  get(id: string, signal?: AbortSignal): Promise<Project>
  create(command: CreateCommand, signal?: AbortSignal): Promise<Receipt>
  register(id: string, command: RegisterCommand, signal?: AbortSignal): Promise<Receipt>
  layout(id: string, command: LayoutCommand, signal?: AbortSignal): Promise<Receipt>
  receipt(id: string, signal?: AbortSignal): Promise<Receipt>
}

/** @param name 契约定义名 @param input 未信任的 JSON @returns 已验证的边界类型。 */
export function parseContract<T>(name: string, input: unknown): T {
  if (!matches<T>(name, input)) throw new RequestError({ code: 'INVALID_RESPONSE', message: '本机服务响应不符合契约，请核实请求结果' }, 0)
  return input
}

/** @param path API 路径 @param name 响应定义 @param method 方法 @param body 原请求体 @param signal 会话取消 @returns 已验证的响应。 */
async function request<T>(path: string, name: string, method = 'GET', body?: object, signal?: AbortSignal): Promise<T> {
  const options: RequestInit = { method, signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(12000)]) : AbortSignal.timeout(12000) }
  if (body) { options.headers = { 'Content-Type': 'application/json', 'X-Twine-Request': '1' }; options.body = JSON.stringify(body) }
  let response: Response
  let data: unknown
  try { response = await fetch(`/api/v1${path}`, options); data = await response.json() }
  catch (cause) { throw new RequestError({ code: 'TRANSPORT_FAILED', message: '未收到可确认的响应，请保留草稿并核实或重试原请求' }, 0, { cause }) }
  if (!response.ok) {
    const failure = parseContract<ErrorResponse>('ErrorResponse', data)
    throw new RequestError(failure.error, response.status)
  }
  return parseContract<T>(name, data)
}

/** 真实同源客户端不存储账号、令牌或第二份本地业务真相。 */
export const PROJECT_API: ProjectAPI = {
  list: signal => request('/projects', 'ProjectList', 'GET', undefined, signal),
  get: (id, signal) => request(`/projects/${id}`, 'Project', 'GET', undefined, signal),
  create: (command, signal) => request('/projects', 'Receipt', 'POST', command, signal),
  register: (id, command, signal) => request(`/projects/${id}/services`, 'Receipt', 'POST', command, signal),
  layout: (id, command, signal) => request(`/projects/${id}/layout`, 'Receipt', 'PUT', command, signal),
  receipt: (id, signal) => request(`/mutations/${id}`, 'Receipt', 'GET', undefined, signal)
}
