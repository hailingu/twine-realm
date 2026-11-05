/** 完整质量门禁：执行当前已接入的检查、采集脚本覆盖率并等待 Sonar 结果。 */
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { loadSonarSettings, redactOutput, runCommand, runSonarGate } from './sonar-gate.mjs'
import { runPlatformChecks } from './platform-check.mjs'
import { assertCleanResult } from './check-command.mjs'
import { isolatedGitEnvironment } from './git-environment.mjs'
import { requiresPreservation } from './runtime-check.mjs'

/** 执行一个真实检查步骤，失败时停止后续扫描。 */
async function checked(command, args, cwd, token, execute, env, write) {
  write(`[QUALITY_STEP] ${command} ${args.join(' ')}\n`)
  const result = await execute(command, args, { cwd, env })
  write(redactOutput(result.output || '', token))
  assertCleanResult(result, command)
}

/** 同一次门禁覆盖工具、平台真实边界与 Sonar，结束后清理验证产物。 */
export async function runQualityGate({
  root = process.cwd(), env = process.env, execute = runCommand, scan = runSonarGate,
  write = message => process.stdout.write(message), platform = runPlatformChecks
} = {}) {
  const checkEnv = isolatedGitEnvironment(env)
  const settings = loadSonarSettings(checkEnv)
  const temporary = await mkdtemp(join(tmpdir(), 'twine-quality-'))
  const check = (command, args, cwd = root) => checked(command, args, cwd, settings.token, execute, checkEnv, write)
  let preserve = false
  try {
    const scriptCoverage = join(temporary, 'scripts-lcov.info')
    // 原生 Node 测试只执行一次，同时输出可读结果及真实脚本覆盖率。
    await check(process.execPath, [
      '--test', '--experimental-test-coverage', '--test-coverage-include=scripts/**/*.mjs',
      '--test-coverage-exclude=**/*.node.mjs', '--test-reporter=spec', '--test-reporter-destination=stdout',
      '--test-reporter=lcov', `--test-reporter-destination=${scriptCoverage}`,
      'scripts/sonar-gate.node.mjs', 'scripts/quality-gate.node.mjs', 'scripts/issue-status.node.mjs', 'scripts/platform-check.node.mjs', 'scripts/check-command.node.mjs', 'scripts/precommit.node.mjs', 'scripts/runtime-check.node.mjs', 'scripts/service-profile.node.mjs'
    ])
    await check(process.execPath, ['scripts/issue-status.mjs'])
    await check('npm', ['audit', '--audit-level=low'])
    await check('npm', ['audit', '--audit-level=low'], join(root, 'web'))
    const reports = await platform({ root, temporary, env: checkEnv, execute, write })
    const lcovReportPaths = [scriptCoverage, ...reports.lcovReportPaths]
    const { output, ...result } = await scan({ root, lcovReportPaths, goCoverageReportPaths: reports.goCoverageReportPaths, env: checkEnv })
    write(`${output}\n[QUALITY_GATE_OK] ${JSON.stringify(result)}\n`)
  } catch (error) { preserve = requiresPreservation(error); throw error
  } finally { if (preserve) write(`运行测试现场保留在 ${temporary}\n`); else await rm(temporary, { recursive: true, force: true }) }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await runQualityGate() } catch (error) {
    process.stderr.write(`${redactOutput(error.message, process.env.SONAR_TOKEN || process.env.TWINE_REALM_SONAR_TOKEN)}\n`)
    process.exitCode = 1
  }
}
