/** 标准 MySQL 只消费与本页面校验器匹配的固定目录，不生成任意字段或秘密输入。 */
import { useEffect, useState } from 'react'
import type { DefinitionCatalog, MiddlewareCommand, MiddlewareNode } from '../contract/generated'
import type { MySQLConfiguration } from '../contract/mysql.generated'
import { MYSQL_DEFAULTS, MYSQL_REFERENCE } from '../contract/catalog.generated'
import { parseContract, type ProjectAPI } from './api'

/** @param api 目录端口 @returns 带错误与取消边界的当前受控目录。 */
function useCatalog(api: ProjectAPI) {
  const [state, setState] = useState<{ value: DefinitionCatalog | null; error: string | null }>({ value: null, error: null })
  useEffect(() => {
    const controller = new AbortController()
    api.catalog(controller.signal).then(value => { if (!controller.signal.aborted) setState({ value, error: null }) }).catch((error: unknown) => { if (!controller.signal.aborted) setState({ value: null, error: error instanceof Error ? error.message : '目录不可读取' }) })
    return () => { controller.abort() }
  }, [api])
  return state
}

/** @param props 目录端口、当前组件与已授权保存入口 @returns 单一固定定义的可审阅草稿表单。 */
export function MySQLPanel({ api, node, blocked, onSave }: Readonly<{ api: ProjectAPI; node: MiddlewareNode | undefined; blocked: boolean; onSave: (input: Omit<MiddlewareCommand, 'mutationId' | 'baseRevision'>) => void }>) {
  const catalog = useCatalog(api)
  const definition = catalog.value?.definitions.find(item => item.reference.id === MYSQL_REFERENCE.id && item.reference.version === MYSQL_REFERENCE.version && item.reference.digest === MYSQL_REFERENCE.digest)
  return <section className="mysql-panel" aria-label="MySQL 配置草稿"><h3>{node ? `${node.displayName} 的配置` : '加入 MySQL 组件'}</h3>
    {catalog.error && <p role="alert" className="notice error">{catalog.error}</p>}
    {!catalog.value && !catalog.error && <p>正在读取受控目录…</p>}
    {catalog.value && !definition && <p role="alert">没有与本页面匹配的 MySQL 定义，请更新页面后重试。</p>}
    {definition && <><small>定义 {definition.reference.id} / {definition.reference.version}</small><code className="definition-digest" title={definition.reference.digest}>{definition.reference.digest}</code><p className="field-help">具名提供端口：{definition.ports.map(port => `${port.id}（${port.protocol} / ${port.contract}）`).join('、')}。配置生效：需替换。</p></>}
    <MySQLForm node={node} blocked={blocked || !definition} onSave={onSave} />
    {node?.notReadyReasons.map(reason => <p key={reason} className="field-help">{reason}</p>)}
    <p className="field-help">保存只建立配置草稿。当前不会启动 MySQL、生成密码或执行部署。</p>
  </section>
}

/** @param node 已保存组件 @returns 已校验配置或明确阻塞，不用缺省值掩盖不兼容旧数据。 */
function readConfiguration(node: MiddlewareNode | undefined): { value: MySQLConfiguration; error: string | null } {
  if (!node) return { value: MYSQL_DEFAULTS, error: null }
  try { return { value: parseContract<MySQLConfiguration>('MySQLConfiguration', node.configuration), error: null } }
  catch { return { value: {}, error: '已保存配置与当前定义不兼容，保留原记录并停止编辑。' } }
}

/** @param props 已保存配置与事件入口 @returns 不因错误或后台读取替换用户候选的本地表单。 */
function MySQLForm({ node, blocked, onSave }: Readonly<{ node: MiddlewareNode | undefined; blocked: boolean; onSave: (input: Omit<MiddlewareCommand, 'mutationId' | 'baseRevision'>) => void }>) {
  const [initial] = useState(() => readConfiguration(node))
  const [name, setName] = useState(node?.displayName ?? 'MySQL')
  const [memory, setMemory] = useState(String(initial.value.memoryMiB ?? MYSQL_DEFAULTS.memoryMiB))
  const [profile, setProfile] = useState(initial.value.runtimeProfile ?? '')
  const [error, setError] = useState<string | null>(null)
  const disabled = blocked || initial.error !== null
  const submit = (event: React.SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (disabled) return
    try {
      const configuration = parseContract<MySQLConfiguration>('MySQLConfiguration', { charset: initial.value.charset ?? MYSQL_DEFAULTS.charset, collation: initial.value.collation ?? MYSQL_DEFAULTS.collation, memoryMiB: Number(memory), ...(profile ? { runtimeProfile: profile } : {}) })
      if (!name.trim()) throw new Error('请填写组件名称')
      setError(null)
      onSave({ definition: MYSQL_REFERENCE, displayName: name.trim(), configuration: { ...configuration }, position: node?.position ?? { x: 320, y: 240 } })
    } catch { setError('配置无效：请检查组件名称、内存整数范围和目录支持的运行方案。') }
  }
  return <form className="mysql-form" onSubmit={submit} noValidate>
    <label>组件名称<input aria-label="MySQL 组件名称" value={name} maxLength={80} disabled={disabled} onChange={event => { setName(event.target.value) }} /></label>
    <label>内存上限 MiB<input aria-label="MySQL 内存 MiB" type="number" min={256} max={8192} step={1} value={memory} disabled={disabled} onChange={event => { setMemory(event.target.value) }} /></label>
    <label>运行方案<select aria-label="MySQL 运行方案" value={profile} disabled={disabled} onChange={event => { setProfile(event.target.value) }}><option value="">尚未选择（可保存草稿）</option><option value="local-compose-v1">本机 Compose v1</option></select></label>
    <p className="field-help">字符集 {MYSQL_DEFAULTS.charset} · 排序规则 {MYSQL_DEFAULTS.collation}。数据库不发布宿主端口，数据使用项目持久卷。</p>
    {(error || initial.error) && <p className="notice error" role="alert">{error ?? initial.error}</p>}
    <button type="submit" disabled={disabled}>{node ? '保存 MySQL 配置' : '确认加入 MySQL'}</button>
  </form>
}
