// 本文件只在实际 schema 位置检查引用，不将业务字段名或 examples 当成 schema 关键字。
package catalog

import (
	"strings"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// checkInlineReferences 校验已知子 schema 位置；真正的语法和词汇仍交给 JSON Schema 编译器。
func checkInlineReferences(value any) error {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if err := checkSchemaKeywords(schema); err != nil {
		return err
	}
	for _, key := range []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "contains", "if", "then", "else", "not", "items", "unevaluatedItems", "contentSchema"} {
		if err := checkInlineReferences(schema[key]); err != nil {
			return err
		}
	}
	for _, key := range []string{"properties", "patternProperties", "dependentSchemas", "$defs", "definitions", "dependencies"} {
		if err := checkSchemaMapping(schema[key]); err != nil {
			return err
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if err := checkSchemaSequence(schema[key]); err != nil {
			return err
		}
	}
	return nil
}

// checkSchemaKeywords 只检查当前 schema 对象的方言和引用关键字。
func checkSchemaKeywords(schema map[string]any) error {
	for _, key := range []string{"$schema", "$ref", "$dynamicRef"} {
		if nested, present := schema[key]; present {
			if err := checkSchemaKeyword(key, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSchemaMapping 在属性或定义字典内继续检查，键本身仍只是业务名称。
func checkSchemaMapping(value any) error {
	children, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, child := range children {
		if err := checkInlineReferences(child); err != nil {
			return err
		}
	}
	return nil
}

// checkSchemaSequence 检查组合与元组的子 schema，不遍历 examples 等普通数据数组。
func checkSchemaSequence(value any) error {
	children, ok := value.([]any)
	if !ok {
		return nil
	}
	for _, child := range children {
		if err := checkInlineReferences(child); err != nil {
			return err
		}
	}
	return nil
}

// checkSchemaKeyword 只接受 2020-12 与当前 inline schema 的片段引用。
func checkSchemaKeyword(key string, value any) error {
	text, ok := value.(string)
	if key == "$schema" {
		if !ok || text != "https://json-schema.org/draft/2020-12/schema" {
			return project.Failure("INVALID_SERVICE_SCHEMA", "只支持 JSON Schema 2020-12")
		}
		return nil
	}
	if !ok || !strings.HasPrefix(text, "#") {
		return project.Failure("INVALID_SERVICE_SCHEMA", "服务 schema 只能引用当前离线 schema 内片段")
	}
	return nil
}
