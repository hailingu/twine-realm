/** 从服务端版本化 JSON Schema 生成类型；检查模式阻止手写类型与契约漂移。 */
import { readFile, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { compile } from 'json-schema-to-typescript'
import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'
import standaloneCode from 'ajv/dist/standalone/index.js'
const input = new URL('../../server/contract/v1/schema.json', import.meta.url)
const target = new URL('../src/contract/generated.d.ts', import.meta.url)
const schema = JSON.parse(await readFile(input, 'utf8'))
const components = JSON.parse(await readFile(new URL('../../server/contract/v1/components.schema.json', import.meta.url), 'utf8'))
const runtime = JSON.parse(await readFile(new URL('../../server/contract/v1/runtime.schema.json', import.meta.url), 'utf8'))
const graph = JSON.parse(await readFile(new URL('../../server/contract/v2/graph.schema.json', import.meta.url), 'utf8'))
const buildFiles = ['build-inputs.schema.json', 'build-artifacts.schema.json', 'build.schema.json']
const builds = await Promise.all(buildFiles.map(async filename => JSON.parse(await readFile(new URL(`../../server/contract/v3/${filename}`, import.meta.url), 'utf8'))))
const deployFiles = ['deployment-inputs.schema.json', 'deployment-plan.schema.json', 'deployment.schema.json']
const deployments = await Promise.all(deployFiles.map(async filename => JSON.parse(await readFile(new URL(`../../server/contract/v4/${filename}`, import.meta.url), 'utf8'))))
const state = JSON.parse(await readFile(new URL('../../server/contract/v5/state.schema.json', import.meta.url), 'utf8'))

const lifecycle = JSON.parse(await readFile(new URL('../../server/contract/v6/lifecycle.schema.json', import.meta.url), 'utf8'))

/** 在内存展开已登记构建/部署分片，禁止从任意外部URL补全类型。 */
function expandFragments(value) {
  let data = JSON.stringify(value).replaceAll('../v1/schema.json#/$defs/', '#/$defs/')
  for (const fragment of [schema, components, runtime, graph, ...builds, ...deployments, state, lifecycle]) data = data.replaceAll(`${fragment.$id}#/$defs/`, '#/$defs/')
  return JSON.parse(data)
}
// 类型生成只在内存展开已知离线分片；磁盘上的 schema 仍为唯一字段权威。
const typeSchema = globalThis.structuredClone(schema)
for (const [filename, fragment] of [['components.schema.json', components], ['runtime.schema.json', runtime]]) {
  for (const [name, value] of Object.entries(fragment.$defs)) {
    if (schema.$defs[name]?.$ref !== `./${filename}#/$defs/${name}`) throw new Error(`重复或未注册的契约定义 ${name}`)
    typeSchema.$defs[name] = JSON.parse(JSON.stringify(value).replaceAll('./schema.json#/$defs/', '#/$defs/'))
  }
}
for (const [name, value] of Object.entries(graph.$defs)) {
  if (Object.hasOwn(typeSchema.$defs, name)) throw new Error(`重复的版本化类型 ${name}`)
  typeSchema.$defs[name] = JSON.parse(JSON.stringify(value).replaceAll('../v1/schema.json#/$defs/', '#/$defs/'))
}
Object.assign(typeSchema.properties, graph.properties)
for (const fragment of builds) for (const [name, value] of Object.entries(fragment.$defs)) {
  if (Object.hasOwn(typeSchema.$defs, name)) throw new Error(`重复的构建类型 ${name}`)
  typeSchema.$defs[name] = expandFragments(value)
}
for (const fragment of deployments) for (const [name, value] of Object.entries(fragment.$defs)) {
  if (Object.hasOwn(typeSchema.$defs, name)) throw new Error(`重复的部署类型 ${name}`)
  typeSchema.$defs[name] = expandFragments(value)
}
Object.assign(typeSchema.properties, expandFragments(builds[2].properties), expandFragments(deployments[2].properties))
for (const [name, value] of Object.entries(state.$defs)) {
  if (Object.hasOwn(typeSchema.$defs, name)) throw new Error(`重复的状态类型 ${name}`)
  typeSchema.$defs[name] = expandFragments(value)
}
Object.assign(typeSchema.properties, expandFragments(state.properties))
for (const [name, value] of Object.entries(lifecycle.$defs)) typeSchema.$defs[name] = expandFragments(value)
Object.assign(typeSchema.properties, expandFragments(lifecycle.properties))
const definitionData = await readFile(new URL('../../server/internal/catalog/definitions/mysql-v1.json', import.meta.url), 'utf8')
const definition = JSON.parse(definitionData)
const digest = 'sha256:' + createHash('sha256').update(definitionData).digest('hex')
const reference = { id: definition.id, version: definition.version, digest }
// 只为类型生成展开同一个已登记配置；运行时仍解析原有离线$id与必需字段。
typeSchema.$defs.RuntimeConfiguration = { ...globalThis.structuredClone(definition.configurationSchema), title: 'RuntimeConfiguration', required: runtime.$defs.RuntimeConfiguration.required }
delete typeSchema.$defs.RuntimeConfiguration.$id
delete typeSchema.$defs.RuntimeConfiguration.$schema
const output = await compile(typeSchema, 'Contract', {
  bannerComment: '/** 由 server/contract/ 的离线版本化 schema 生成，请运行 npm run contract；禁止手工修改。 */',
  // 数组数量仍由原 schema 和独立运行时校验器检查，类型不展开巨大的 tuple 并集。
  style: { singleQuote: true, semi: false }, additionalProperties: false, maxItems: -1
})
const validator = new Ajv2020({ strict: true, code: { source: true } })
addFormats(validator)
validator.addSchema(schema)
validator.addSchema(components)
validator.addSchema(runtime)
validator.addSchema(graph)
for (const fragment of builds) validator.addSchema(fragment)
for (const fragment of deployments) validator.addSchema(fragment)
validator.addSchema(state)
validator.addSchema(lifecycle)
validator.addSchema(definition.configurationSchema)
const references = Object.fromEntries(['Project', 'ProjectList', 'Receipt', 'ErrorResponse', 'SourceObservation', 'SourceReceipt', 'DefinitionCatalog', 'CanvasSnapshot', 'CanvasReceipt', 'RuntimeCommand', 'RuntimeReceipt', 'RuntimeSnapshot', 'RuntimeOperation', 'RuntimeCapability', 'RuntimeEventPage'].map(name => [name, `${schema.$id}#/$defs/${name}`]))
references.MySQLConfiguration = definition.configurationSchema.$id
for (const name of ['ServiceContractDocument', 'GraphOperation', 'GraphEditCommand', 'GraphSnapshot', 'GraphReceipt']) references[name] = `${graph.$id}#/$defs/${name}`
for (const name of ['BuildCommand', 'BuildCancelCommand', 'BuildOperation', 'BuildReceipt', 'BuildSnapshot', 'BuildCapability']) references[name] = `${builds[2].$id}#/$defs/${name}`
for (const [index, names] of [['DeployPreviewRequest', 'DeployCommand'], ['DeployPlan'], ['DeployOperation', 'DeployReceipt', 'DeploySnapshot', 'DeployCapability']].entries()) for (const name of names) references[name] = `${deployments[index].$id}#/$defs/${name}`
for (const name of ['StateSnapshot', 'StateEvent', 'StateCaughtUp', 'StateResync']) references[name] = `${state.$id}#/$defs/${name}`
for (const name of Object.keys(lifecycle.$defs)) references[name] = `${lifecycle.$id}#/$defs/${name}`
const validators = '/** 由共享 JSON Schema 构建生成；浏览器不执行动态代码编译。 */\n' + standaloneCode(validator, references)
const declaration = '/** 生成校验器的边界声明；运行时执行同一 schema 生成的检查。 */\nimport type { ValidateFunction } from "ajv"\ndeclare const validators: Record<string, ValidateFunction<unknown>>\nexport = validators\n'
const configuration = await compile(definition.configurationSchema, 'MySQLConfiguration', { bannerComment: '/** 由离线 MySQL 定义生成，禁止手工修改。 */', style: { singleQuote: true, semi: false } })
const defaults = Object.fromEntries(Object.entries(definition.configurationSchema.properties).filter(([, value]) => Object.hasOwn(value, 'default')).map(([key, value]) => [key, value.default]))
const metadata = '/** 由同一离线目录生成；定义与校验器不匹配时阻止新组件编辑。 */\n' + `export const MYSQL_REFERENCE = ${JSON.stringify(reference)} as const\nexport const CATALOG_DIGEST = ${JSON.stringify(digest)}\nexport const MYSQL_DEFAULTS = ${JSON.stringify(defaults)} as const\n`
const example = JSON.parse(await readFile(new URL('../../server/contract/v2/fixtures/service-contract.json', import.meta.url), 'utf8'))
const graphMetadata = '/** 显式服务契约示例来自共享测试材料；不表示任何服务源码已实现。 */\n' + `export const SERVICE_CONTRACT_EXAMPLE = ${JSON.stringify(example)} as const\nexport const MYSQL_PORTS = ${JSON.stringify(definition.ports)} as const\n`
const generated = [{ file: target, content: output }, { file: new URL('../src/contract/validators.cjs', import.meta.url), content: validators }, { file: new URL('../src/contract/validators.d.cts', import.meta.url), content: declaration }, { file: new URL('../src/contract/mysql.generated.d.ts', import.meta.url), content: configuration }, { file: new URL('../src/contract/catalog.generated.ts', import.meta.url), content: metadata }, { file: new URL('../src/contract/graph.generated.ts', import.meta.url), content: graphMetadata }]
for (const { file, content } of generated) {
  if (process.argv.includes('--check')) {
    if (await readFile(file, 'utf8') !== content) throw new Error('契约类型或校验器已过期，请运行 npm run contract')
  } else { await writeFile(file, content) }
}
