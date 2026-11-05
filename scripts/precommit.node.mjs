/** 在真实临时 Git 仓库验证提交拒绝、索引一致性和自定义 hook 保护。 */
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { inspectIndex, runPrecommit } from './precommit.mjs'
import { installHooks } from './install-hooks.mjs'
import { runCommand } from './sonar-gate.mjs'
import { isolatedGitEnvironment } from './git-environment.mjs'

/** 仅在测试拥有的仓库建立索引，不在真实项目暂存或提交。 */
async function fixture(use) {
  const root = await mkdtemp(join(tmpdir(), 'twine-precommit-test-'))
  const env = isolatedGitEnvironment()
  const git = args => execFileSync('git', args, { cwd: root, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  try { git(['init', '-b', 'test']); await mkdir(join(root, '.githooks')); await copyFile(new URL('../.githooks/pre-commit', import.meta.url), join(root, '.githooks/pre-commit')); await use(root, git, env) }
  finally { await rm(root, { recursive: true, force: true }) }
}

test('未完整暂存的源码被拒绝，检查期间索引变化也被拒绝', async () => {
  await fixture(async (root, git, env) => {
    await mkdir(join(root, 'server')); await writeFile(join(root, 'server', 'sample.go'), 'package sample\n')
    assert.throws(() => inspectIndex(root, env), /PRECOMMIT_INDEX_MISMATCH/)
    git(['add', '.'])
    await assert.doesNotReject(runPrecommit({ root, env, staged: true, check: async () => {} }))
    await assert.rejects(runPrecommit({ root, env, staged: true, check: async () => { await writeFile(join(root, 'server', 'sample.go'), 'package changed\n'); git(['add', '.']) } }), /PRECOMMIT_INDEX_CHANGED/)
  })
})

test('真实 Git hook 运行零告警检查后拒绝有告警的提交，不产生 HEAD', async () => {
  await fixture(async (root, git, env) => {
    const checker = new URL('./check-command.mjs', import.meta.url).href
    await writeFile(join(root, 'package.json'), JSON.stringify({ private: true, type: 'module', scripts: { precommit: 'node guard.mjs' } }))
    await writeFile(join(root, 'guard.mjs'), `import { checkCommand } from ${JSON.stringify(checker)}\nawait checkCommand(process.execPath, ['-e', 'process.stderr.write("file.go:1:1: warning: controlled")'], {})\n`)
    await installHooks(root); git(['add', '.'])
    const result = await runCommand('git', ['-c', 'user.name=测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', '受控拒绝夹具'], { cwd: root, env })
    assert.notEqual(result.code, 0)
    assert.match(result.output, /QUALITY_WARNING_REJECTED/)
    assert.throws(() => git(['rev-parse', '--verify', 'HEAD']))
    assert.equal(git(['config', '--local', 'core.hooksPath']).trim(), '.githooks')
    await chmod(join(root, '.githooks', 'pre-commit'), 0o755)
  })
})

/** 在自有 linked worktree 的真实 hook 中执行嵌套 Git，核对父索引和配置。 */
async function writeIsolationScenario(root) {
  const gate = new URL('./quality-gate.mjs', import.meta.url).href
  const command = new URL('./sonar-gate.mjs', import.meta.url).href
  await writeFile(join(root, 'package.json'), JSON.stringify({ private: true, type: 'module', scripts: { precommit: 'node guard.mjs' } }))
  await writeFile(join(root, 'nested-check.mjs'), `
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
assert.equal(Object.keys(process.env).some(key => key.startsWith('GIT_')), false)
const root = realpathSync(mkdtempSync(join(tmpdir(), 'twine-nested-git-')))
const git = args => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
try {
  git(['init', '-b', 'nested']); git(['config', '--local', 'core.hooksPath', 'nested-hooks'])
  writeFileSync(join(root, 'owned.txt'), '嵌套夹具'); git(['add', '.'])
  assert.equal(git(['rev-parse', '--show-toplevel']).trim(), root)
  assert.throws(() => git(['rev-parse', '--verify', 'HEAD']))
} finally { rmSync(root, { recursive: true, force: true }) }
`)
  await writeFile(join(root, 'guard.mjs'), `
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { runQualityGate } from ${JSON.stringify(gate)}
import { runCommand } from ${JSON.stringify(command)}
const git = args => execFileSync('git', args, { encoding: 'utf8' }).trim()
assert.ok(Object.keys(process.env).some(key => key.startsWith('GIT_')))
const paths = ['index', 'config'].map(name => resolve(git(['rev-parse', '--git-path', name])))
const before = paths.map(path => readFileSync(path))
await runQualityGate({
  env: { ...process.env, SONAR_HOST_URL: 'http://sonar.invalid', SONAR_TOKEN: 'synthetic-hook-token' },
  execute: (command, args, settings) => args.includes('--test') ? runCommand(process.execPath, ['nested-check.mjs'], settings) : Promise.resolve({ code: 0, output: '' }),
  platform: async () => ({ lcovReportPaths: [], goCoverageReportPaths: [] }),
  scan: async () => ({ status: 'OK', output: '' }), write: message => process.stdout.write(message)
})
assert.deepEqual(paths.map(path => readFileSync(path)), before)
process.stdout.write('[GIT_ISOLATION_OK]\\n')
`)
}

test('真实 linked-worktree hook 的嵌套 Git 检查不污染父索引、配置或分支', async () => {
  await fixture(async (root, git, env) => {
    git(['-c', 'user.name=测试', '-c', 'user.email=test@example.invalid', 'commit', '--allow-empty', '-m', '夹具基线'])
    const baseline = git(['rev-parse', 'HEAD'])
    const work = join(root, 'linked')
    git(['worktree', 'add', '-b', 'linked', work])
    await mkdir(join(work, '.githooks')); await copyFile(new URL('../.githooks/pre-commit', import.meta.url), join(work, '.githooks/pre-commit'))
    await writeIsolationScenario(work); await installHooks(work)
    git(['-C', work, 'add', '.'])
    const result = await runCommand('git', ['-C', work, '-c', 'user.name=测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', '隔离后通过'], { env })
    assert.equal(result.code, 0, result.output)
    assert.match(result.output, /GIT_ISOLATION_OK/)
    assert.equal(git(['rev-parse', 'HEAD']), baseline)
    assert.notEqual(git(['-C', work, 'rev-parse', 'HEAD']), baseline)
    assert.equal(git(['config', '--local', 'core.bare']).trim(), 'false')
  })
})

test('已有自定义 hook 配置不被安装器覆盖', async () => {
  await fixture(async (root, git) => {
    git(['config', '--local', 'core.hooksPath', 'existing-hooks'])
    await assert.rejects(installHooks(root), /未覆盖/)
    assert.equal(git(['config', '--local', 'core.hooksPath']).trim(), 'existing-hooks')
    assert.ok((await readFile(join(root, '.githooks', 'pre-commit'), 'utf8')).includes('precommit'))
  })
})

test('默认目录已有 pre-commit 时安装器保留原文件和流程', async () => {
  await fixture(async (root, git) => {
    const path = join(root, '.git', 'hooks', 'pre-commit')
    const content = '#!/bin/sh\nexit 17\n'
    await writeFile(path, content)
    await assert.rejects(installHooks(root), /已有默认 pre-commit/)
    assert.equal(await readFile(path, 'utf8'), content)
    assert.throws(() => git(['config', '--local', '--get', 'core.hooksPath']))
  })
})
