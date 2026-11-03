/** 完整质量门禁：执行当前已接入的检查、采集脚本覆盖率并等待 Sonar 结果。 */
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { loadSonarSettings, redactOutput, runCommand, runSonarGate } from './sonar-gate.mjs'

/** 执行一个真实检查步骤，失败时停止后续扫描。 */
async function checked(command, args, cwd, token, execute, env, write) {
  write(`[QUALITY_STEP] ${command} ${args.join(' ')}\n`)
  const result = await execute(command, args, { cwd, env })
  write(redactOutput(result.output || '', token))
  if (result.code !== 0) throw new Error(`[QUALITY_CHECK_FAILED] ${command} ${args.join(' ')} 未通过。`)
}

/** 在本次专属目录测试和扫描，结束后清理验证产物；typecheck、lint、构建等模块检查在对应工程初始化后接入。 */
export async function runQualityGate({
  root = process.cwd(), env = process.env, execute = runCommand, scan = runSonarGate,
  write = message => process.stdout.write(message)
} = {}) {
  const settings = loadSonarSettings(env)
  const temporary = await mkdtemp(join(tmpdir(), 'twine-quality-'))
  const check = (command, args, cwd = root) => checked(command, args, cwd, settings.token, execute, env, write)
  try {
    const scriptCoverage = join(temporary, 'scripts-lcov.info')
    // 原生 Node 测试只执行一次，同时输出可读结果及真实脚本覆盖率。
    await check(process.execPath, [
      '--test', '--experimental-test-coverage', '--test-coverage-include=scripts/**/*.mjs',
      '--test-coverage-exclude=**/*.node.mjs', '--test-reporter=spec', '--test-reporter-destination=stdout',
      '--test-reporter=lcov', `--test-reporter-destination=${scriptCoverage}`,
      'scripts/sonar-gate.node.mjs', 'scripts/quality-gate.node.mjs', 'scripts/issue-status.node.mjs'
    ])
    await check(process.execPath, ['scripts/issue-status.mjs'])
    const lcovReportPaths = [scriptCoverage]
    const { output, ...result } = await scan({ root, lcovReportPaths, env })
    write(`${output}\n[QUALITY_GATE_OK] ${JSON.stringify(result)}\n`)
  } finally { await rm(temporary, { recursive: true, force: true }) }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await runQualityGate() } catch (error) {
    process.stderr.write(`${redactOutput(error.message, process.env.SONAR_TOKEN || process.env.TWINE_REALM_SONAR_TOKEN)}\n`)
    process.exitCode = 1
  }
}
