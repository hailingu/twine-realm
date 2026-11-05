/** 安装仓库本地提交入口，已有自定义 hook 配置时明确停止，不覆盖用户配置。 */
import { execFileSync, spawnSync } from 'node:child_process'
import { chmod, readFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isolatedGitEnvironment } from './git-environment.mjs'

/** 仅设置当前仓库的 hooksPath，配置冲突必须由所有者明确处理。 */
export async function installHooks(root = process.cwd()) {
  const configuration = { cwd: root, encoding: 'utf8', env: isolatedGitEnvironment() }
  const result = spawnSync('/usr/bin/git', ['config', '--get', 'core.hooksPath'], configuration)
  if (result.status !== 0 && result.status !== 1) throw new Error('无法读取仓库 hook 配置')
  const existing = result.stdout.trim()
  if (existing && existing !== '.githooks') throw new Error('已有自定义 core.hooksPath，未覆盖：' + existing)
  if (!existing) {
    const legacy = execFileSync('/usr/bin/git', ['rev-parse', '--git-path', 'hooks/pre-commit'], configuration).trim()
    try {
      await readFile(resolve(root, legacy))
      throw new Error('已有默认 pre-commit，未覆盖；须先合并现有流程')
    } catch (error) { if (error.code !== 'ENOENT') throw error }
  }
  await chmod(join(root, '.githooks', 'pre-commit'), 0o755)
  execFileSync('/usr/bin/git', ['config', '--local', 'core.hooksPath', '.githooks'], configuration)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await installHooks(); process.stdout.write('[HOOKS_INSTALLED] 本仓库 pre-commit 已启用\n') }
  catch (error) { process.stderr.write(error.message + '\n'); process.exitCode = 1 }
}
