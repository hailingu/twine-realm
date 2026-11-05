/** 浏览器验收使用真实平台进程与 Git 夹具；重启复用同一个数据库。 */
import { test as base } from '@playwright/test'
import { spawn, execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { promisify } from 'node:util'
import type { ChildProcess } from 'node:child_process'

const execute = promisify(execFile)

/** 临时平台拥有者只操作自己的服务进程与目录。 */
export interface Platform {
  url: string
  root: string
  restart(): Promise<void>
  repository(name: string, commit?: boolean): Promise<string>
  fingerprint(path: string): Promise<string>
  git(path: string, args: string[]): Promise<string>
}

/** @returns 可用于本机隔离平台的空闲端口。 */
async function availablePort(): Promise<number> {
  const server = createServer()
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const address = server.address()
  await new Promise<void>((resolve, reject) => { server.close(error => { if (error) reject(error); else resolve() }) })
  if (!address || typeof address === 'string') throw new Error('无法确定测试端口')
  return address.port
}

/** @param path 真实测试仓库 @returns 覆盖源文件、AGENTS、Git 索引与配置的内容摘要。 */
async function fingerprint(path: string): Promise<string> {
  const hash = createHash('sha256')
  const visit = async (directory: string) => {
    for (const item of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
      const target = join(directory, item.name)
      hash.update(target)
      if (item.isDirectory()) await visit(target)
      else hash.update(await readFile(target))
    }
  }
  await visit(path)
  return hash.digest('hex')
}

/** @param root 测试允许目录 @returns 可启动、停止并重启真实平台的夹具。 */
async function createPlatform(root: string): Promise<Platform & { stop(): Promise<void> }> {
  const port = await availablePort()
  const address = `127.0.0.1:${port}`
  const url = `http://${address}`
  const binary = process.env.TWINE_TEST_BINARY
  if (!binary || !process.env.TWINE_DATABASE_DSN) throw new Error('浏览器测试须由根 npm run test:platform 提供临时数据库和平台二进制')
  let child: ChildProcess | undefined
  const stop = async () => {
    const current = child
    if (!current || current.exitCode !== null) return
    const done = new Promise<void>((resolve, reject) => { current.once('exit', code => { if (code === 0) resolve(); else reject(new Error('测试平台非正常退出')) }) })
    current.kill('SIGTERM')
    try { await Promise.race([done, delay(15000).then(() => { throw new Error('测试平台退出超时') })]) }
    catch (error) { current.kill('SIGKILL'); throw error }
    finally { child = undefined }
  }
  const start = async () => {
    child = spawn(binary, [], { cwd: resolve('..'), env: { ...process.env, TWINE_ADDRESS: address, TWINE_REPOSITORY_ROOTS: JSON.stringify([root]), TWINE_WEB_DIRECTORY: process.env.TWINE_TEST_WEB_DIRECTORY ?? resolve('dist') }, stdio: ['ignore', 'ignore', 'pipe'] })
    let diagnostic = ''; child.stderr?.on('data', (data: Buffer) => { diagnostic = (diagnostic + data.toString()).slice(-4096) })
    let launchError: Error | undefined; child.once('error', error => { launchError = error })
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      if (launchError || child.exitCode !== null) throw new Error(`测试平台启动失败：${diagnostic}`, { cause: launchError })
      try { const response = await fetch(`${url}/healthz`, { signal: AbortSignal.timeout(500) }); if (response.ok) return }
      catch { /* 进程尚未开放监听，继续以就绪信号核实。 */ }
      await delay(100)
    }
    throw new Error('测试平台未就绪')
  }
  await start()
  return { url, root, stop, fingerprint, git: async (path, args) => (await execute('git', ['-C', path, ...args])).stdout.trim(), restart: async () => { await stop(); await start() }, repository: async (name, commit = true) => {
    const path = join(root, name, 'service'); await mkdir(path, { recursive: true })
    await execute('git', ['-C', path, 'init', '-b', 'develop'])
    await writeFile(join(path, 'AGENTS.md'), '用户维护的服务规范\n')
    await writeFile(join(path, 'source.txt'), '业务源码保持原样\n')
    if (commit) { await execute('git', ['-C', path, 'add', '.']); await execute('git', ['-C', path, '-c', 'user.name=测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', '浏览器夹具']) }
    return path
  } }
}

/** 浏览器用例共享串行平台实例，结束后等待进程退出再清理测试仓库。 */
export const test = base.extend<object, { platform: Platform }>({
  platform: [async ({ browserName }, provide) => {
    const directory = await mkdtemp(join(tmpdir(), `twine-browser-${browserName}-`))
    let platform: Awaited<ReturnType<typeof createPlatform>> | undefined
    try { platform = await createPlatform(await realpath(directory)); await provide(platform) }
    finally { if (platform) await platform.stop(); await rm(directory, { recursive: true, force: true }) }
  }, { scope: 'worker' }]
})
