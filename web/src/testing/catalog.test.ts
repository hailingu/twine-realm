/** 前后端共享的真实配置样本验证相同定义、方言与边界，不复制一套规则。 */
import { expect, it } from 'vitest'
import samples from '../../../server/internal/catalog/definitions/configuration-cases.json'
import { parseContract } from '../project/api'

for (const sample of samples) {
  it(`MySQL 目录样本：${sample.name}`, () => {
    const validate = () => parseContract('MySQLConfiguration', sample.configuration)
    if (sample.valid) expect(validate).not.toThrow()
    else expect(validate).toThrow()
  })
}
