/** 生成校验器的边界声明；运行时执行同一 schema 生成的检查。 */
import type { ValidateFunction } from "ajv"
declare const validators: Record<string, ValidateFunction<unknown>>
export = validators
