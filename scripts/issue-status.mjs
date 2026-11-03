/** issue 状态检查：解析 issues/ 文件头，校验元数据、编号与日期一致，并报告状态分布与未关闭记录。 */
import { readdir, readFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { lexer } from 'marked'

/** 问题记录文件名：编号固定四位，短名不可为空；不合规的候选文件必须报告错误。 */
const ISSUE_FILE_PATTERN = /^ISSUE-(\d{4})-.+\.md$/
const FIELD_PATTERN = /^(\w+):\s*"(.*)"$/
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/
const DISPOSITION_DATE_PATTERN = /^(\d{4}-\d{2}-\d{2})(?=[\s：（(]|$)/gm
const REQUIRED_FIELDS = ['id', 'title', 'type', 'status', 'priority', 'scope', 'createdAt', 'updatedAt']
/** 纯枚举用集合承载成员判定；状态数组额外驱动有序报告。 */
const ISSUE_TYPES = new Set(['缺陷', '功能', '工程债务', '已知边界'])
/** 状态按生命周期顺序报告；已关闭视为完结，其余均属未关闭。 */
const ISSUE_STATUSES = ['待处理', '进行中', '待验证', '已关闭', '暂缓']
const ISSUE_PRIORITIES = new Set(['P1', 'P2', 'P3'])
/** 范围枚举与 [问题记录规范](../docs/development/issue-standard.md) 同步；跨域记录可用 + 联写。 */
const ISSUE_SCOPES = new Set(['server', 'web', 'deploy', 'design', 'docs', 'tooling'])

/** 解析 issue 状态检查参数，只接受 --json 且拒绝未知或重复参数。 */
export function parseIssueStatusArgs(args) {
  const options = { json: false }
  for (const argument of args) {
    if (argument === '--json' && !options.json) options.json = true
    else throw new Error(`未知或重复参数: ${argument}`)
  }
  return options
}

/** 提取带引号标量构成的文件头与正文；缺失或字段行不可解析时返回 null。 */
export function parseFrontmatter(text) {
  const match = text.match(/^---\n([\s\S]*?)\n---\n/)
  if (!match) return null
  const metadata = {}
  for (const line of match[1].split('\n')) {
    const field = line.match(FIELD_PATTERN)
    if (!field) return null
    metadata[field[1]] = field[2]
  }
  return { metadata, body: text.slice(match[0].length) }
}

/** 校验 YYYY-MM-DD 形式且真实存在的公历日期，字符串比较依赖该规范格式。 */
function isValidDate(value) {
  if (!DATE_PATTERN.test(value)) return false
  const [year, month, day] = value.split('-').map(Number)
  const date = new Date(Date.UTC(year, month - 1, day))
  return date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day
}

/** 校验必填字段存在且非空白；owner 不在必填清单，允许缺失或空串。 */
function validateRequiredFields(metadata) {
  const problems = []
  for (const field of REQUIRED_FIELDS) {
    if (!metadata[field] || metadata[field].trim() === '') problems.push(`缺必填字段 ${field}`)
  }
  return problems
}

/** 校验 id 与文件名编号一致；id 缺失时跳过，由必填字段校验报告。 */
function validateNumber(number, metadata) {
  if (!metadata.id) return []
  return metadata.id === `ISSUE-${number}` ? [] : [`id ${metadata.id} 与文件名编号不符`]
}

/** 校验 type、status 与 priority 三个枚举字段。 */
function validateEnums(metadata) {
  const problems = []
  if (!ISSUE_TYPES.has(metadata.type)) problems.push(`非法 type ${metadata.type}`)
  if (!ISSUE_STATUSES.includes(metadata.status)) problems.push(`非法 status ${metadata.status}`)
  if (!ISSUE_PRIORITIES.has(metadata.priority)) problems.push(`非法 priority ${metadata.priority}`)
  return problems
}

/** 校验标题 P 级别前缀及其与 priority 的一致性。 */
function validateTitle(metadata) {
  if (!metadata.title) return []
  const title = metadata.title.match(/^P([123])\(/)
  if (!title) return [`标题缺 P<1|2|3>( 前缀: ${metadata.title}`]
  return `P${title[1]}` === metadata.priority ? [] : [`标题级别 P${title[1]} 与 priority ${metadata.priority} 不一致`]
}

/** 校验 scope 各段均在枚举内；跨域必须使用 + 联写。 */
function validateScope(metadata) {
  if (!metadata.scope) return []
  return metadata.scope
    .split('+')
    .filter(part => !ISSUE_SCOPES.has(part))
    .map(part => `非法 scope 段 ${part}（跨域使用 + 联写）`)
}

/** 只提取处置记录章节的正文与下级标题日期；Markdown 代码示例不参与比较。 */
function lastDispositionDate(body) {
  let sectionDepth = 0
  const dates = []
  for (const token of lexer(body)) {
    if (token.type === 'heading') {
      if (token.depth <= sectionDepth) sectionDepth = 0
      if (token.text === '处置记录') sectionDepth = token.depth
    }
    if (sectionDepth === 0 || !['paragraph', 'heading'].includes(token.type)) continue
    dates.push(...Array.from(token.text.matchAll(DISPOSITION_DATE_PATTERN), match => match[1]))
  }
  return dates.toSorted((left, right) => left.localeCompare(right)).at(-1)
}

/** 校验两个日期字段的 YYYY-MM-DD 格式与真实公历日期。 */
function validateDateFormats(metadata) {
  const problems = []
  for (const field of ['createdAt', 'updatedAt']) {
    if (metadata[field] && !isValidDate(metadata[field])) problems.push(`${field} 非法日期 ${metadata[field]}`)
  }
  return problems
}

/** 校验 updatedAt 不早于创建日期；仅在两者格式合法时比较。 */
function validateDateOrder(metadata) {
  if (!isValidDate(metadata.createdAt) || !isValidDate(metadata.updatedAt)) return []
  return metadata.updatedAt < metadata.createdAt
    ? [`updatedAt ${metadata.updatedAt} 早于 createdAt ${metadata.createdAt}`]
    : []
}

/** 校验 updatedAt 不早于正文最后一条处置记录。 */
function validateDispositionOrder(metadata, body) {
  const lastDisposition = lastDispositionDate(body)
  if (!lastDisposition || !isValidDate(metadata.updatedAt)) return []
  return metadata.updatedAt < lastDisposition
    ? [`updatedAt ${metadata.updatedAt} 早于最后处置记录 ${lastDisposition}`]
    : []
}

/** 对单条记录执行全部元数据校验，返回原因列表；问题记录必须整体可定位。 */
function validateIssueRecord(number, metadata, body) {
  return [
    ...validateRequiredFields(metadata),
    ...validateNumber(number, metadata),
    ...validateEnums(metadata),
    ...validateTitle(metadata),
    ...validateScope(metadata),
    ...validateDateFormats(metadata),
    ...validateDateOrder(metadata),
    ...validateDispositionOrder(metadata, body)
  ]
}

/** 扫描 issues/ 下全部问题记录并校验，返回记录清单与逐文件问题列表。 */
export async function collectIssueStatuses({ root = process.cwd(), issuesDirectory } = {}) {
  const directory = issuesDirectory ?? join(root, 'issues')
  const files = (await readdir(directory))
    .filter(file => file.startsWith('ISSUE-') && file.endsWith('.md'))
    .sort((left, right) => left.localeCompare(right))
  const records = []
  const problems = []
  const seen = new Map()
  for (const file of files) {
    const filename = ISSUE_FILE_PATTERN.exec(file)
    if (!filename) {
      problems.push(`${file}：文件名格式错误，应为 ISSUE-0001-中文短名.md`)
      continue
    }
    const number = filename[1]
    const parsed = parseFrontmatter(await readFile(join(directory, file), 'utf8'))
    if (!parsed) {
      problems.push(`${file}：文件头缺失或存在非带引号标量的字段行`)
      continue
    }
    for (const problem of validateIssueRecord(number, parsed.metadata, parsed.body)) {
      problems.push(`${file}：${problem}`)
    }
    if (seen.has(parsed.metadata.id)) problems.push(`${file}：编号重复 ${parsed.metadata.id}（${seen.get(parsed.metadata.id)}）`)
    seen.set(parsed.metadata.id, file)
    records.push({ file, ...parsed.metadata })
  }
  return { records, problems }
}

/** 汇总状态计数与未关闭清单，按编号稳定排序。 */
function summarizeRecords(records) {
  const counts = Object.fromEntries(ISSUE_STATUSES.map(status => [status, 0]))
  for (const record of records) counts[record.status] += 1
  const open = records
    .filter(record => record.status !== '已关闭')
    .sort((left, right) => left.id.localeCompare(right.id))
    .map(({ id, status, priority, title }) => ({ id, status, priority, title }))
  return { total: records.length, counts, open }
}

/** 输出人类可读的状态报告；未关闭记录是正常工作流状态，不构成失败。 */
function writeTextReport(write, summary) {
  const counted = ISSUE_STATUSES.filter(status => summary.counts[status] > 0)
    .map(status => `${status} ${summary.counts[status]}`)
  write(`[ISSUE_STATUS] 扫描 issues/：${summary.total} 个问题记录\n`)
  write(`状态统计：${counted.join('、')}\n`)
  if (summary.open.length === 0) write('未关闭记录：无\n')
  else {
    write('未关闭记录：\n')
    for (const record of summary.open) write(`- ${record.id}（${record.status}，${record.priority}）${record.title}\n`)
  }
  write('[ISSUE_STATUS_OK] 元数据校验通过\n')
}

/** 检查 issues/ 元数据与状态：校验失败抛出全部原因，成功输出报告并返回摘要。 */
export async function runIssueStatus({
  root = process.cwd(), issuesDirectory, json = false,
  write = message => process.stdout.write(message)
} = {}) {
  const { records, problems } = await collectIssueStatuses({ root, issuesDirectory })
  if (problems.length > 0) {
    throw new Error(`[ISSUE_STATUS_FAILED] issues/ 元数据校验失败（${problems.length} 项）：\n${problems.join('\n')}`)
  }
  const summary = summarizeRecords(records)
  if (json) write(`${JSON.stringify(summary)}\n`)
  else writeTextReport(write, summary)
  return summary
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    await runIssueStatus(parseIssueStatusArgs(process.argv.slice(2)))
  } catch (error) {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  }
}
