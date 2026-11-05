// Package contract 共享发布的版本化机器契约，供 HTTP 校验和前端类型生成消费。
package contract

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/catalog"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed v1/schema.json v1/*.schema.json
var files embed.FS

// Load 编译当前公开请求定义；失败会阻止应用启动。
func Load() (map[string]*jsonschema.Schema, error) {
	entries, err := files.ReadDir("v1")
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	compiler.LoadURL = func(string) (io.ReadCloser, error) { return nil, fmt.Errorf("接口契约不解析外部引用") }
	if err := registerCatalogSchemas(compiler); err != nil {
		return nil, err
	}
	const location = "https://twine-realm.local/contract/v1/schema.json"
	for _, entry := range entries {
		data, err := files.ReadFile("v1/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if err := compiler.AddResource("https://twine-realm.local/contract/v1/"+entry.Name(), bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}
	result := map[string]*jsonschema.Schema{}
	for _, name := range []string{"CreateCommand", "RegisterCommand", "LayoutCommand", "Project", "ProjectList", "Receipt", "ErrorResponse", "PinSourceCommand", "SourceObservation", "SourceReceipt", "MiddlewareCommand", "CanvasLayoutCommand", "DefinitionCatalog", "CanvasSnapshot", "CanvasReceipt", "RuntimeCommand", "RuntimeReceipt", "RuntimeSnapshot", "RuntimeOperation", "RuntimeCapability", "RuntimeEventPage"} {
		schema, err := compiler.Compile(location + "#/$defs/" + name)
		if err != nil {
			return nil, fmt.Errorf("编译请求契约失败: %w", err)
		}
		result[name] = schema
	}
	return result, nil
}

// registerCatalogSchemas 只注册随平台发布的离线配置，运行契约不复制另一套字段约束。
func registerCatalogSchemas(compiler *jsonschema.Compiler) error {
	definitions, err := catalog.New()
	if err != nil {
		return fmt.Errorf("运行契约的固定目录不可用")
	}
	for _, definition := range definitions.List().Definitions {
		var header struct {
			ID string `json:"$id"`
		}
		if json.Unmarshal(definition.ConfigurationSchema, &header) != nil || header.ID == "" {
			return fmt.Errorf("目录配置缺少固定schema身份")
		}
		if err := compiler.AddResource(header.ID, bytes.NewReader(definition.ConfigurationSchema)); err != nil {
			return fmt.Errorf("目录配置无法注册到运行契约")
		}
	}
	return nil
}
