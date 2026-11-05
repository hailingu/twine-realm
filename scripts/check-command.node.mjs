/** 使用真实子进程和诊断样本验证零告警门禁，不能以退出码零冒充通过。 */
import assert from 'node:assert/strict'
import test from 'node:test'
import { assertCleanResult, checkCommand } from './check-command.mjs'

test('代码诊断为告警时，即使工具返回零也拒绝', async () => {
  for (const diagnostic of ['file.go:1:1: warning: controlled', 'warning TS1234: controlled', '[plugin:example] warning: controlled', '(node:1) DeprecationWarning: controlled', 'ld: warning: controlled', '(!) controlled build diagnostic', 'npm warn controlled', 'Encountered two children with the same key, controlled.', 'Each child in a list should have a unique "key" prop.']) {
    await assert.rejects(checkCommand(process.execPath, ['-e', 'process.stderr.write(process.argv[1])', diagnostic], {}, { write: () => {} }), /QUALITY_WARNING_REJECTED/)
  }
})

test('普通提示允许继续，工具错误保持失败且不被告警文本替换', () => {
  assert.doesNotThrow(() => assertCleanResult({ code: 0, output: '编译完成\ngo: downloading example\n' }, 'go'))
  assert.throws(() => assertCleanResult({ code: 2, output: '类型检查失败' }, 'tsc'), /QUALITY_CHECK_FAILED/)
})
