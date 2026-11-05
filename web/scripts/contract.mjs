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
// 类型生成只在内存展开已知离线分片；磁盘上的 schema 仍为唯一字段权威。
const typeSchema = globalThis.structuredClone(schema)
for (const [name, value] of Object.entries(components.$defs)) {
  if (schema.$defs[name]?.$ref !== `./components.schema.json#/$defs/${name}`) throw new Error(`重复或未注册的契约定义 ${name}`)
  typeSchema.$defs[name] = JSON.parse(JSON.stringify(value).replaceAll('./schema.json#/$defs/', '#/$defs/'))
}
const definitionData = await readFile(new URL('../../server/internal/catalog/definitions/mysql-v1.json', import.meta.url), 'utf8')
const definition = JSON.parse(definitionData)
const digest = 'sha256:' + createHash('sha256').update(definitionData).digest('hex')
const reference = { id: definition.id, version: definition.version, digest }
const output = await compile(typeSchema, 'Contract', {
  bannerComment: '/** 由 server/contract/v1/schema.json 生成，请运行 npm run contract；禁止手工修改。 */',
  // 数组数量仍由原 schema 和独立运行时校验器检查，类型不展开巨大的 tuple 并集。
  style: { singleQuote: true, semi: false }, additionalProperties: false, maxItems: -1
})
const validator = new Ajv2020({ strict: true, code: { source: true } })
addFormats(validator)
validator.addSchema(schema)
validator.addSchema(components)
validator.addSchema(definition.configurationSchema)
const references = Object.fromEntries(['Project', 'ProjectList', 'Receipt', 'ErrorResponse', 'SourceObservation', 'SourceReceipt', 'DefinitionCatalog', 'CanvasSnapshot', 'CanvasReceipt'].map(name => [name, `${schema.$id}#/$defs/${name}`]))
references.MySQLConfiguration = definition.configurationSchema.$id
const validators = '/** 由共享 JSON Schema 构建生成；浏览器不执行动态代码编译。 */\n' + standaloneCode(validator, references)
const declaration = '/** 生成校验器的边界声明；运行时执行同一 schema 生成的检查。 */\nimport type { ValidateFunction } from "ajv"\ndeclare const validators: Record<string, ValidateFunction<unknown>>\nexport = validators\n'
const configuration = await compile(definition.configurationSchema, 'MySQLConfiguration', { bannerComment: '/** 由离线 MySQL 定义生成，禁止手工修改。 */', style: { singleQuote: true, semi: false } })
const defaults = Object.fromEntries(Object.entries(definition.configurationSchema.properties).filter(([, value]) => Object.hasOwn(value, 'default')).map(([key, value]) => [key, value.default]))
const metadata = '/** 由同一离线目录生成；定义与校验器不匹配时阻止新组件编辑。 */\n' + `export const MYSQL_REFERENCE = ${JSON.stringify(reference)} as const\nexport const CATALOG_DIGEST = ${JSON.stringify(digest)}\nexport const MYSQL_DEFAULTS = ${JSON.stringify(defaults)} as const\n`
const generated = [{ file: target, content: output }, { file: new URL('../src/contract/validators.cjs', import.meta.url), content: validators }, { file: new URL('../src/contract/validators.d.cts', import.meta.url), content: declaration }, { file: new URL('../src/contract/mysql.generated.d.ts', import.meta.url), content: configuration }, { file: new URL('../src/contract/catalog.generated.ts', import.meta.url), content: metadata }]
for (const { file, content } of generated) {
  if (process.argv.includes('--check')) {
    if (await readFile(file, 'utf8') !== content) throw new Error('契约类型或校验器已过期，请运行 npm run contract')
  } else { await writeFile(file, content) }
}
