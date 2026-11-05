/** 从服务端版本化 JSON Schema 生成类型；检查模式阻止手写类型与契约漂移。 */
import { readFile, writeFile } from 'node:fs/promises'
import { compile } from 'json-schema-to-typescript'
import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'
import standaloneCode from 'ajv/dist/standalone/index.js'
const input = new URL('../../server/contract/v1/schema.json', import.meta.url)
const target = new URL('../src/contract/generated.d.ts', import.meta.url)
const schema = JSON.parse(await readFile(input, 'utf8'))
const output = await compile(schema, 'Contract', {
  bannerComment: '/** 由 server/contract/v1/schema.json 生成，请运行 npm run contract；禁止手工修改。 */',
  style: { singleQuote: true, semi: false }, additionalProperties: false
})
const validator = new Ajv2020({ strict: true, code: { source: true } })
addFormats(validator)
validator.addSchema(schema)
const references = Object.fromEntries(['Project', 'ProjectList', 'Receipt', 'ErrorResponse'].map(name => [name, `${schema.$id}#/$defs/${name}`]))
const validators = '/** 由共享 JSON Schema 构建生成；浏览器不执行动态代码编译。 */\n' + standaloneCode(validator, references)
const declaration = '/** 生成校验器的边界声明；运行时执行同一 schema 生成的检查。 */\nimport type { ValidateFunction } from "ajv"\ndeclare const validators: Record<string, ValidateFunction<unknown>>\nexport = validators\n'
const generated = [{ file: target, content: output }, { file: new URL('../src/contract/validators.cjs', import.meta.url), content: validators }, { file: new URL('../src/contract/validators.d.cts', import.meta.url), content: declaration }]
for (const { file, content } of generated) {
  if (process.argv.includes('--check')) {
    if (await readFile(file, 'utf8') !== content) throw new Error('契约类型或校验器已过期，请运行 npm run contract')
  } else { await writeFile(file, content) }
}
