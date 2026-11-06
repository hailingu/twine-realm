// Package contract 共享发布的版本化机器契约，供 HTTP 校验和前端类型生成消费。
package contract

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed v1/schema.json v1/*.schema.json v2/*.schema.json v3/*.schema.json v4/*.schema.json
var files embed.FS

// Load 编译当前公开请求定义；失败会阻止应用启动。
func Load() (map[string]*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	compiler.LoadURL = func(string) (io.ReadCloser, error) { return nil, fmt.Errorf("接口契约不解析外部引用") }
	if err := registerCatalogSchemas(compiler); err != nil {
		return nil, err
	}
	if err := registerResources(compiler); err != nil {
		return nil, err
	}
	result := map[string]*jsonschema.Schema{}
	for _, group := range []struct {
		location string
		names    []string
	}{
		{"v1/schema.json", []string{"CreateCommand", "RegisterCommand", "LayoutCommand", "Project", "ProjectList", "Receipt", "ErrorResponse", "PinSourceCommand", "SourceObservation", "SourceReceipt", "MiddlewareCommand", "CanvasLayoutCommand", "DefinitionCatalog", "CanvasSnapshot", "CanvasReceipt", "RuntimeCommand", "RuntimeReceipt", "RuntimeSnapshot", "RuntimeOperation", "RuntimeCapability", "RuntimeEventPage"}},
		{"v2/graph.schema.json", []string{"ServiceContractDocument", "GraphEditCommand", "GraphSnapshot", "GraphReceipt"}},
		{"v3/build.schema.json", []string{"BuildCommand", "BuildCancelCommand", "BuildOperation", "BuildReceipt", "BuildSnapshot", "BuildCapability"}},
		{"v4/deployment-inputs.schema.json", []string{"DeployPreviewRequest", "DeployCommand"}},
		{"v4/deployment-plan.schema.json", []string{"DeployPlan"}},
		{"v4/deployment.schema.json", []string{"DeployOperation", "DeployReceipt", "DeploySnapshot", "DeployCapability"}},
	} {
		for _, name := range group.names {
			schema, err := compiler.Compile("https://twine-realm.local/contract/" + group.location + "#/$defs/" + name)
			if err != nil {
				return nil, fmt.Errorf("编译请求契约失败: %w", err)
			}
			result[name] = schema
		}
	}
	return result, nil
}

// registerResources 仅注册二进制内的离线分片，任意外部 URL 或文件不能补全契约。
func registerResources(compiler *jsonschema.Compiler) error {
	return fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		return compiler.AddResource("https://twine-realm.local/contract/"+path, bytes.NewReader(data))
	})
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
