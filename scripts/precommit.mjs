/** 提交前验证完整门禁；Git 入口另外核对待提交内容，绝不替用户暂存或提交。 */
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runQualityGate } from './quality-gate.mjs'
import { redactOutput } from './sonar-gate.mjs'

const CODE_PATH = /^(?:server\/|web\/|scripts\/|\.github\/|\.githooks\/|deploy\/|package(?:-lock)?\.json$|sonar-project\.properties$|\.nvmrc$|\.gitignore$)/

/** 校验全部受检源码 / 配置与索引一致，阻止部分暂存导致检查对象错位。 */
export function inspectIndex(root, env = process.env) {
  const git = args => execFileSync('/usr/bin/git', ['--no-optional-locks', ...args], { cwd: root, env, encoding: 'utf8' })
  const changed = [...git(['diff', '--name-only', '-z']).split('\0'), ...git(['ls-files', '--others', '--exclude-standard', '-z']).split('\0')]
  const mismatches = changed.filter(path => CODE_PATH.test(path))
  if (mismatches.length) throw new Error('[PRECOMMIT_INDEX_MISMATCH] 受检源码或配置尚未完整暂存，请先同步工作区与索引：\n' + mismatches.join('\n'))
  return createHash('sha256').update(git(['ls-files', '--stage', '-z'])).digest('hex')
}

/** 执行完整检查，并在检查期间索引或源码变化时拒绝继续提交。 */
export async function runPrecommit({ root = process.cwd(), staged = false, check = runQualityGate, env = process.env } = {}) {
  const index = staged ? inspectIndex(root, env) : null
  await check({ root, env })
  if (staged && inspectIndex(root, env) !== index) throw new Error('[PRECOMMIT_INDEX_CHANGED] 检查期间待提交内容变化，请重新运行门禁')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await runPrecommit({ staged: process.argv.includes('--staged') }); process.stdout.write('[PRECOMMIT_OK] 本次检查通过，未执行提交\n') }
  catch (error) { process.stderr.write(redactOutput(error.message, process.env.SONAR_TOKEN || process.env.TWINE_REALM_SONAR_TOKEN) + '\n'); process.exitCode = 1 }
}
