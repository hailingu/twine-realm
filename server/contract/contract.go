// Package contract 共享发布的版本化机器契约，供 HTTP 校验和前端类型生成消费。
package contract

import (
	"bytes"
	"embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed v1/schema.json
var files embed.FS

// Load 编译当前公开请求定义；失败会阻止应用启动。
func Load() (map[string]*jsonschema.Schema, error) {
	data, err := files.ReadFile("v1/schema.json")
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	const location = "https://twine-realm.local/contract/v1/schema.json"
	if err := compiler.AddResource(location, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	result := map[string]*jsonschema.Schema{}
	for _, name := range []string{"CreateCommand", "RegisterCommand", "LayoutCommand", "Project", "ProjectList", "Receipt", "ErrorResponse"} {
		schema, err := compiler.Compile(location + "#/$defs/" + name)
		if err != nil {
			return nil, fmt.Errorf("编译请求契约失败: %w", err)
		}
		result[name] = schema
	}
	return result, nil
}
