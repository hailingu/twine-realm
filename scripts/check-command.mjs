/** 统一代码检查：非零结果或明确告警诊断均阻断，不隐藏原始告警。 */
import { runCommand } from './sonar-gate.mjs'

/** 分行解析固定诊断前缀，避免对无界输出使用会回溯的复杂正则。 */
function isWarningLine(input) {
  let line = input.trimStart()
  if (line.startsWith('[')) {
    const closing = line.indexOf(']')
    if (closing >= 0) line = line.slice(closing + 1).trimStart()
  }
  const lower = line.toLowerCase()
  return lower.startsWith('(!) ') || lower.startsWith('npm warn ') || /^warn(?:ing)?\b/i.test(line) ||
    /\bwarning(?: TS\d+|:)/i.test(line) || lower.includes('deprecationwarning') || lower.includes('experimentalwarning') ||
    lower.startsWith('encountered two children with the same key,') || lower.startsWith('each child in a list should have a unique "key" prop')
}

/** 校验退出结果及可识别的编译器 / 工具告警；普通提示不伪装成代码告警。 */
export function assertCleanResult(result, label) {
  if (result.code !== 0) throw new Error(`[QUALITY_CHECK_FAILED] ${label} 未通过`)
  const diagnostic = result.output || ''
  const plain = diagnostic.replace(/\u001b\[[\d;]*m/g, '')
  if (plain.split('\n').some(isWarningLine)) {
    throw new Error(`[QUALITY_WARNING_REJECTED] ${label} 产生告警，必须修复后才能提交`)
  }
}

/** 执行真实检查并在退出码为零但仍产生告警时失败。 */
export async function checkCommand(command, args, configuration, { execute = runCommand, write = message => process.stdout.write(message) } = {}) {
  const result = await execute(command, args, configuration)
  write(result.output || '')
  assertCleanResult(result, command)
  return result.output || ''
}
