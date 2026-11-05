// Package catalog 发布离线固定定义，并以同一配置 schema 校验和补齐默认值。
package catalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed definitions/mysql-v1.json
var files embed.FS

// Catalog 只持有随平台发布并已编译的定义，不从用户路径或网络加载插件。
type Catalog struct {
	definition project.NodeDefinition
	schema     *jsonschema.Schema
	defaults   map[string]any
	digest     string
}

// New 在平台开放 HTTP 前验证本地包；外部引用一律拒绝。
func New() (*Catalog, error) {
	data, err := files.ReadFile("definitions/mysql-v1.json")
	if err != nil {
		return nil, err
	}
	var raw struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		project.NodeDefinition
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	definition := raw.NodeDefinition
	definition.Reference = project.DefinitionRef{ID: raw.ID, Version: raw.Version, Digest: digest}
	schema, err := compileConfiguration(definition.ConfigurationSchema)
	if err != nil {
		return nil, err
	}
	defaults, err := configurationDefaults(definition.ConfigurationSchema)
	if err != nil {
		return nil, err
	}
	return &Catalog{definition: definition, schema: schema, defaults: defaults, digest: digest}, nil
}

// compileConfiguration 只解析当前定义材料，任何外部文件或网络引用都不能补全配置。
func compileConfiguration(data []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(string) (io.ReadCloser, error) { return nil, fmt.Errorf("目录禁止解析外部 schema 引用") }
	const location = "https://twine-realm.local/catalog/mysql/1.0.0/configuration"
	if err := compiler.AddResource(location, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return compiler.Compile(location)
}

// configurationDefaults 显式提取注解，校验器不会被当作自动修改输入的机制。
func configurationDefaults(data []byte) (map[string]any, error) {
	var shape struct {
		Properties map[string]struct {
			Default any `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, err
	}
	result := map[string]any{}
	for key, value := range shape.Properties {
		if value.Default != nil {
			result[key] = value.Default
		}
	}
	return result, nil
}

// List 返回当前受控发布目录；前端用内容摘要核对本次内置校验器。
func (catalog *Catalog) List() project.DefinitionCatalog {
	return project.DefinitionCatalog{SchemaVersion: 1, Digest: catalog.digest, Definitions: []project.NodeDefinition{catalog.definition}}
}

// Normalize 对新对象补缺省值，拒绝未知定义/版本/摘要、字段或非法已填值。
func (catalog *Catalog) Normalize(reference project.DefinitionRef, configuration map[string]any) (map[string]any, []string, error) {
	if reference != catalog.definition.Reference {
		return nil, nil, project.Failure("UNSUPPORTED_DEFINITION", "组件定义、版本或摘要不受当前离线目录支持")
	}
	if configuration == nil {
		return nil, nil, project.Failure("INVALID_CONFIGURATION", "配置必须为非敏感对象")
	}
	result := map[string]any{}
	for key, value := range configuration {
		result[key] = value
	}
	for key, value := range catalog.defaults {
		if _, present := result[key]; !present {
			result[key] = value
		}
	}
	if err := catalog.schema.Validate(result); err != nil {
		return nil, nil, project.Failure("INVALID_CONFIGURATION", "MySQL 配置包含非法值或未声明字段，请核实后重试")
	}
	reasons := []string{}
	for _, key := range catalog.definition.RequiredForDeployment {
		if _, present := result[key]; !present {
			reasons = append(reasons, "缺少运行必需配置："+key)
		}
	}
	return result, reasons, nil
}
