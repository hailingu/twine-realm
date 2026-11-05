/** 用真实打包器和 ESLint warning 证明提交检查会拒绝告警。 */
import { afterEach, expect, it } from 'vitest'
import { build } from 'vite'
import { execFile } from 'node:child_process'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { promisify } from 'node:util'
import { createRequire } from 'node:module'
import type * as MinimatchModule from 'minimatch'
import { createWarningFreeLogger } from '../../tooling/warningLogger'

const execute = promisify(execFile)
const temporary: string[] = []
afterEach(async () => { for (const path of temporary.splice(0)) await rm(path, { recursive: true, force: true }) })

it('真实 Vite 插件告警会让构建失败', async () => {
  const root = await mkdtemp(join(tmpdir(), 'twine-warning-build-')); temporary.push(root)
  await writeFile(join(root, 'entry.js'), 'export const value = 1\n')
  await expect(build({ root, configFile: false, customLogger: createWarningFreeLogger(), plugins: [{ name: 'controlled-warning', buildStart() { this.warn('受控构建告警') } }], build: { write: false, rolldownOptions: { input: join(root, 'entry.js') } } })).rejects.toThrow('构建告警必须修复')
})

it('真实 ESLint 只产生 warning 时，max-warnings 0 仍以非零退出', async () => {
  const path = resolve('src/testing/warningProbe.temporary.ts'); temporary.push(path)
  await writeFile(path, '/** 受控告警夹具，测试结束删除。 */\nexport {}\nconst warningProbe = 1\n')
  try {
    await execute(process.execPath, ['node_modules/eslint/bin/eslint.js', path, '--rule', '@typescript-eslint/no-unused-vars:warn', '--max-warnings', '0'])
    throw new Error('告警未被阻断')
  } catch (error) {
    expect(error).toMatchObject({ code: 1 })
    expect(error).toHaveProperty('stdout', expect.stringMatching(/1 warning/))
    expect(error).toHaveProperty('stderr', expect.stringContaining('too many warnings'))
  }
})

it('修复后的 minimatch 保持三个实际 lint 消费方的具名导出与路径语义', () => {
  for (const consumer of ['eslint', '@eslint/config-array', '@typescript-eslint/typescript-estree']) {
    const require = createRequire(resolve('package.json'))
    const atConsumer = createRequire(require.resolve(consumer))
    const module: unknown = atConsumer('minimatch')
    if (!isMinimatchModule(module)) throw new Error('实际 lint 消费方缺少所需具名导出')
    const { minimatch, Minimatch } = module
    expect(module).toHaveProperty('minimatch')
    expect(minimatch('src/project/App.tsx', 'src/**/*.{ts,tsx}')).toBe(true)
    expect(minimatch('node_modules/example/index.ts', 'src/**/*.{ts,tsx}')).toBe(false)
    expect(new Minimatch('**/node_modules/**', { dot: true }).match('web/node_modules/example/index.js')).toBe(true)
  }
})

/** @param value 消费方实际加载的未知模块 @returns 是否提供需要验证的函数边界。 */
function isMinimatchModule(value: unknown): value is typeof MinimatchModule {
  return typeof value === 'object' && value !== null && 'minimatch' in value && typeof value.minimatch === 'function' && 'Minimatch' in value && typeof value.Minimatch === 'function'
}
