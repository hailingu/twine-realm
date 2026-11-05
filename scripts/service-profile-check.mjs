/** 在本次新目录实际导出、检查、构建并运行受信模板，不执行任意用户仓库代码。 */
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { checkCommand } from './check-command.mjs'
import { runCommand } from './sonar-gate.mjs'

/** 模板构建只继承工具与缓存位置，不继承 Sonar、模型、云端或外层 Git 凭据。 */
function toolEnvironment(env) {
  const result = Object.fromEntries(['PATH', 'HOME', 'TMPDIR', 'GOCACHE', 'GOMODCACHE', 'GOPATH', 'HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY'].filter(key => env[key] !== undefined).map(key => [key, env[key]]))
  return { ...result, GOTOOLCHAIN: 'local', GOWORK: 'off', GOFLAGS: '-mod=readonly', FORCE_COLOR: '0' }
}

/** 返回本次模板单测 / 真实进程覆盖率；temporary 的删除由外层验证所有者负责。 */
export async function runServiceProfileChecks({ root, temporary, adminDSN, env = process.env, execute = runCommand, write = value => process.stdout.write(value) }) {
  if (!adminDSN) throw new Error('模板真实验证需要本次自有数据库配置')
  const project = join(temporary, 'service-template')
  const cli = join(temporary, 'service-profile')
  const binary = join(temporary, 'service-example')
  const testReport = join(temporary, 'service-test.out')
  const binaryDirectory = join(temporary, 'service-binary-coverage')
  const binaryReport = join(temporary, 'service-binary.out')
  const tools = toolEnvironment(env)
  const checked = async (command, args, cwd, environment = tools) => {
    write(`[SERVICE_PROFILE_STEP] ${command} ${args.join(' ')}\n`)
    return checkCommand(command, args, { cwd, env: environment, timeoutMs: 600000 }, { execute, write })
  }
  await checked(process.execPath, ['scripts/service-profile.mjs', '--check'], root, env)
  await checked('go', ['build', '-cover', '-coverpkg=./...', '-o', cli, './cmd/service-profile'], join(root, 'server'), env)
  await checked(cli, ['export', '--directory', project], root, env)
  await checked(cli, ['validate', '--directory', project], root, env)
  const version = (await checked('go', ['env', 'GOVERSION'], project)).trim()
  if (version !== 'go1.27.3') throw new Error('业务模板仅验证 Go 1.27.3，不能用其他工具链冒充通过')
  if ((await checked('gofmt', ['-l', '.'], project)).trim()) throw new Error('业务模板格式检查未通过')
  await checked('go', ['mod', 'download'], project)
  await checked('go', ['mod', 'verify'], project)
  await checked('go', ['vet', '-tags=integration', './...'], project)
  await mkdir(binaryDirectory)
  await checked('go', ['build', '-mod=readonly', '-trimpath', '-cover', '-coverpkg=./...', '-o', binary, './cmd/service'], project)
  // 管理配置仅交给已核实的内置模板集成测试，子服务环境由测试显式隔离。
  await checked('go', ['test', '-count=1', '-race', '-tags=integration', '-coverpkg=./...', `-coverprofile=${testReport}`, './...'], project, { ...tools, TWINE_TEMPLATE_ADMIN_DSN: adminDSN, TWINE_TEMPLATE_TEST_BINARY: binary, TWINE_TEMPLATE_COVER_DIRECTORY: binaryDirectory })
  await checked('go', ['tool', 'covdata', 'textfmt', `-i=${binaryDirectory}`, `-o=${binaryReport}`], project)
  return { goCoverageReportPaths: [testReport, binaryReport] }
}
