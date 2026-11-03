/** 验证 issue 状态检查的元数据校验、排除规则、报告输出与真实目录守卫。 */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdir, mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { collectIssueStatuses, parseFrontmatter, parseIssueStatusArgs, runIssueStatus } from './issue-status.mjs'

/** 以一份合法记录为基线生成问题文件，覆盖项用于构造各错误类。 */
async function writeIssue(issues, file, overrides = {}, body = '## 处置记录\n\n2026-11-01：**处置**：示例记录。\n') {
  const metadata = {
    id: 'ISSUE-0001',
    title: 'P2(tooling): 示例问题摘要',
    type: '缺陷',
    status: '已关闭',
    priority: 'P2',
    scope: 'tooling',
    createdAt: '2026-11-01',
    updatedAt: '2026-11-01',
    ...overrides
  }
  const frontmatter = Object.entries(metadata).map(([key, value]) => `${key}: "${value}"`).join('\n')
  await writeFile(join(issues, file), `---\n${frontmatter}\n---\n\n# ${metadata.title}\n\n${body}`)
}

/** 建立包含 issues/ 子目录的一次性临时仓库根，配合 finally 清理。 */
async function createIssueRoot() {
  const root = await mkdtemp(join(tmpdir(), 'twine-issue-status-'))
  const issues = join(root, 'issues')
  await mkdir(issues)
  return { root, issues }
}

/** 从临时仓库启动真实 CLI，验证退出码、诊断与 JSON 报告的恢复行为。 */
function runIssueStatusCli(root) {
  const script = fileURLToPath(new URL('./issue-status.mjs', import.meta.url))
  return spawnSync(process.execPath, [script, '--json'], { cwd: root, encoding: 'utf8' })
}

test('参数解析只接受单个 --json，拒绝未知或重复参数', () => {
  assert.deepEqual(parseIssueStatusArgs([]), { json: false })
  assert.deepEqual(parseIssueStatusArgs(['--json']), { json: true })
  assert.throws(() => parseIssueStatusArgs(['--json', '--json']), /未知或重复参数/)
  assert.throws(() => parseIssueStatusArgs(['--quiet']), /未知或重复参数/)
})

test('文件头解析提取带引号标量并保留正文，缺失或畸形文件头返回 null', () => {
  const parsed = parseFrontmatter('---\nid: "ISSUE-0001"\nstatus: "待处理"\n---\n\n正文')
  assert.deepEqual(parsed.metadata, { id: 'ISSUE-0001', status: '待处理' })
  assert.equal(parsed.body, '\n正文')
  assert.equal(parseFrontmatter('# 无文件头\n'), null)
  assert.equal(parseFrontmatter('---\nid: 裸值\n---\n'), null)
})

test('合法目录通过校验并输出状态统计与未关闭清单', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-已关闭示例.md')
    await writeIssue(issues, 'ISSUE-0002-待处理示例.md', { id: 'ISSUE-0002', status: '待处理' })
    // README 与模板不匹配记录文件名模式，即使元数据非法也不参与校验。
    await writeFile(join(issues, 'README.md'), '---\nstatus: 非法\n---\n')
    await writeFile(join(issues, '_template.md'), '---\nstatus: 非法\n---\n')
    const output = []
    const summary = await runIssueStatus({ root, write: message => output.push(message) })
    assert.equal(summary.total, 2)
    assert.equal(summary.counts['已关闭'], 1)
    assert.equal(summary.counts['待处理'], 1)
    assert.deepEqual(summary.open.map(record => record.id), ['ISSUE-0002'])
    const text = output.join('')
    assert.match(text, /状态统计：待处理 1、已关闭 1/)
    assert.match(text, /- ISSUE-0002（待处理，P2）P2\(tooling\): 示例问题摘要/)
    assert.match(text, /\[ISSUE_STATUS_OK\] 元数据校验通过/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('缺必填字段、非法枚举与标题级别不一致各自指明文件与原因', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-缺字段.md', { status: '' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /ISSUE-0001-缺字段\.md：缺必填字段 status/)
    await rm(join(issues, 'ISSUE-0001-缺字段.md'))
    await writeIssue(issues, 'ISSUE-0001-非法枚举.md', { status: '开放' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /非法 status 开放/)
    await rm(join(issues, 'ISSUE-0001-非法枚举.md'))
    await writeIssue(issues, 'ISSUE-0001-非法类型.md', { type: '需求' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /非法 type 需求/)
    await rm(join(issues, 'ISSUE-0001-非法类型.md'))
    await writeIssue(issues, 'ISSUE-0001-非法优先级.md', { priority: 'P0' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /非法 priority P0/)
    await rm(join(issues, 'ISSUE-0001-非法优先级.md'))
    await writeIssue(issues, 'ISSUE-0001-标题无前缀.md', { title: '修复画布问题' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /标题缺 P<1\|2\|3>\( 前缀: 修复画布问题/)
    await rm(join(issues, 'ISSUE-0001-标题无前缀.md'))
    await writeIssue(issues, 'ISSUE-0001-标题级别.md', { title: 'P1(tooling): 摘要' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /标题级别 P1 与 priority P2 不一致/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('编号与文件名不符或重复编号被拒绝', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0002-编号不符.md', { id: 'ISSUE-0009' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /id ISSUE-0009 与文件名编号不符/)
    // 第二个文件声明同一 id 才构成编号冲突；其文件名编号同时不再匹配。
    await writeIssue(issues, 'ISSUE-0001-重复编号.md', { id: 'ISSUE-0009' })
    const { problems } = await collectIssueStatuses({ root })
    assert.ok(problems.some(problem => problem.includes('编号重复 ISSUE-0009（ISSUE-0001-重复编号.md）')),
      `应报告编号重复，实际：${problems.join('；')}`)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('日期非法、早于创建日期或早于最后处置记录均被拒绝', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-日期非法.md', { createdAt: '2026-02-30' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /createdAt 非法日期 2026-02-30/)
    await rm(join(issues, 'ISSUE-0001-日期非法.md'))
    await writeIssue(issues, 'ISSUE-0001-日期格式.md', { updatedAt: '2026/11/01' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /updatedAt 非法日期 2026\/11\/01/)
    await rm(join(issues, 'ISSUE-0001-日期格式.md'))
    await writeIssue(issues, 'ISSUE-0001-早于创建.md', { updatedAt: '2026-10-31' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /updatedAt 2026-10-31 早于 createdAt 2026-11-01/)
    await rm(join(issues, 'ISSUE-0001-早于创建.md'))
    await writeIssue(issues, 'ISSUE-0001-早于处置.md', {},
      '## 处置记录\n\n2026-11-03：**处置**：补记提交证据。\n')
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /updatedAt 2026-11-01 早于最后处置记录 2026-11-03/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('scope 逗号连写或含未知段被拒绝，加号联写的合法跨域通过', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-逗号连写.md', { scope: 'web,server' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /非法 scope 段 web,server/)
    await rm(join(issues, 'ISSUE-0001-逗号连写.md'))
    await writeIssue(issues, 'ISSUE-0001-未知段.md', { scope: 'server+storage' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /非法 scope 段 storage/)
    await rm(join(issues, 'ISSUE-0001-未知段.md'))
    await writeIssue(issues, 'ISSUE-0001-合法跨域.md', { scope: 'server+web' })
    const summary = await runIssueStatus({ root, write: () => {} })
    assert.equal(summary.total, 1)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('--json 输出可整体解析的结构化摘要', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-示例.md', { status: '进行中' })
    const output = []
    const summary = await runIssueStatus({ root, json: true, write: message => output.push(message) })
    const parsed = JSON.parse(output.join(''))
    assert.deepEqual(parsed, summary)
    assert.equal(parsed.open[0].status, '进行中')
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('字段整行缺失与两位数年份怪癖均被拒绝', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    // 整行缺失 scope（区别于空串）：必填字段缺失必须报告，不允许崩溃或静默通过。
    await writeFile(join(issues, 'ISSUE-0001-整行缺失.md'),
      '---\nid: "ISSUE-0001"\ntitle: "P2(tooling): 示例"\ntype: "缺陷"\nstatus: "已关闭"\npriority: "P2"\ncreatedAt: "2026-11-01"\nupdatedAt: "2026-11-01"\n---\n\n# 示例\n')
    const missing = await collectIssueStatuses({ root })
    assert.ok(missing.problems.some(problem => problem.includes('缺必填字段 scope')),
      `应报告缺失字段，实际：${missing.problems.join('；')}`)
    await rm(join(issues, 'ISSUE-0001-整行缺失.md'))
    // Date 将 0-99 年映射到 1900+，格式合法的 0099 年必须按非法日期拒绝。
    await writeIssue(issues, 'ISSUE-0001-年份怪癖.md', { createdAt: '0099-12-31' })
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /createdAt 非法日期 0099-12-31/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('多条处置记录取最后一条，updatedAt 早于其中较晚日期即被拒绝', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-多条处置.md', { updatedAt: '2026-11-03' },
      '## 处置记录\n\n2026-10-31：**处置**：创建记录。\n\n2026-11-05：**处置**：补记证据。\n')
    await assert.rejects(runIssueStatus({ root, write: () => {} }), /updatedAt 2026-11-03 早于最后处置记录 2026-11-05/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

// 回归目标：日期后的括号说明和 Markdown 日期标题不能绕过 updatedAt 检查。
for (const [name, entry] of [
  ['括号说明', '2026-11-03（修复并关闭）：**处置**：关闭。'],
  ['日期标题', '### 2026-11-03 关闭\n\n- **处置**：关闭。']
]) {
  test(`处置记录的${name}日期晚于 updatedAt 时被拒绝`, async () => {
    const { root, issues } = await createIssueRoot()
    try {
      await writeIssue(issues, 'ISSUE-0001-日期写法.md', {},
        `## 处置记录\n\n2026-11-01：**处置**：登记。\n\n${entry}\n`)
      await assert.rejects(runIssueStatus({ root, write: () => {} }), /早于最后处置记录 2026-11-03/)
    } finally { await rm(root, { recursive: true, force: true }) }
  })
}

test('其他章节与处置记录内代码示例的日期不参与 updatedAt 比较', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    const body = [
      '## 计划', '', '2026-11-05：计划复查。', '',
      '## 处置记录', '', '2026-11-01：**处置**：登记。', '',
      '```markdown', '2026-11-06：仅为格式示例。', '## 示例标题', '```', '',
      '### 2026-11-01 验证', '', '已验证。', '',
      '## 后续计划', '', '2026-11-07：计划再次复查。', ''
    ].join('\n')
    await writeIssue(issues, 'ISSUE-0001-章节边界.md', {}, body)
    const summary = await runIssueStatus({ root, write: () => {} })
    assert.equal(summary.total, 1)
    assert.equal(summary.counts['已关闭'], 1)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('没有处置记录章节时不把正文计划日期作为处置日期', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-尚无处置.md', { status: '待处理' },
      '## 计划\n\n2026-11-05：计划开始处理。\n')
    const summary = await runIssueStatus({ root, write: () => {} })
    assert.deepEqual(summary.open.map(record => record.id), ['ISSUE-0001'])
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('处置章节前的引言日期不参与比较，正文无需以一级标题开头', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    const file = join(issues, 'ISSUE-0001-正文引言.md')
    await writeIssue(issues, 'ISSUE-0001-正文引言.md')
    const text = (await readFile(file, 'utf8')).replace('# P2(tooling): 示例问题摘要', '2026-11-05：正文引言。')
    await writeFile(file, text)
    const summary = await runIssueStatus({ root, write: () => {} })
    assert.equal(summary.total, 1)
    assert.equal(summary.counts['已关闭'], 1)
  } finally { await rm(root, { recursive: true, force: true }) }
})

// 回归目标：文件发现阶段不得静默丢弃编号位数或短名格式错误的候选记录。
for (const file of ['ISSUE-2-待处理.md', 'ISSUE-00002-待处理.md', 'ISSUE-0002-.md']) {
  test(`CLI 报告文件名错误 ${file}，修正后恢复未关闭统计`, async () => {
    const { root, issues } = await createIssueRoot()
    try {
      await writeIssue(issues, 'ISSUE-0001-已关闭.md')
      await writeIssue(issues, file, { id: 'ISSUE-0002', status: '待处理' })
      const failed = runIssueStatusCli(root)
      assert.equal(failed.status, 1)
      assert.ok(failed.stderr.includes(file), '诊断必须定位格式错误的文件')
      assert.match(failed.stderr, /文件名格式错误/)
      assert.equal(failed.stdout, '')
      await rename(join(issues, file), join(issues, 'ISSUE-0002-待处理.md'))
      const recovered = runIssueStatusCli(root)
      assert.equal(recovered.status, 0, recovered.stderr)
      const summary = JSON.parse(recovered.stdout)
      assert.equal(summary.total, 2)
      assert.equal(summary.counts['待处理'], 1)
      assert.deepEqual(summary.open, [{
        id: 'ISSUE-0002', status: '待处理', priority: 'P2', title: 'P2(tooling): 示例问题摘要'
      }])
    } finally { await rm(root, { recursive: true, force: true }) }
  })
}

test('非法日期只报告格式问题，不参与排序比较产生误导原因', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-非法创建日.md', { createdAt: '9999-99-99' })
    const created = await collectIssueStatuses({ root })
    assert.deepEqual(created.problems, ['ISSUE-0001-非法创建日.md：createdAt 非法日期 9999-99-99'])
    await rm(join(issues, 'ISSUE-0001-非法创建日.md'))
    await writeIssue(issues, 'ISSUE-0001-非法更新日.md', { updatedAt: '2026-01-99' },
      '## 处置记录\n\n2026-11-01：**处置**：示例记录。\n')
    const updated = await collectIssueStatuses({ root })
    assert.deepEqual(updated.problems, ['ISSUE-0001-非法更新日.md：updatedAt 非法日期 2026-01-99'])
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('显式传入空 issues 目录必须报错，不静默回退默认目录', async () => {
  const { root, issues } = await createIssueRoot()
  try {
    await writeIssue(issues, 'ISSUE-0001-示例.md')
    await assert.rejects(collectIssueStatuses({ root, issuesDirectory: '' }), /ENOENT|Cannot read|readdir/)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('真实仓库 issues/ 目录元数据全部合法（常驻守卫，漂移即失败）', async () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const summary = await runIssueStatus({ root, write: () => {} })
  // 目录允许暂无记录（守卫在首条记录出现后开始约束数量），但已存在的记录必须零问题且编号唯一。
  assert.ok(summary.total >= 0)
  const { records, problems } = await collectIssueStatuses({ root })
  assert.deepEqual(problems, [])
  const ids = records.map(record => record.id)
  assert.equal(new Set(ids).size, ids.length, '真实目录编号必须唯一')
})
