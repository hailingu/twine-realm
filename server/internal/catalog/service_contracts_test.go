// 本文件验证真实服务 schema 编译、多个接口版本、内容固定与离线边界。
package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// serviceDocument 读取前端示例与后端测试共用的显式契约，示例不代表源码已经实现。
func serviceDocument(t *testing.T) project.ServiceContractDocument {
	t.Helper()
	data, err := os.ReadFile("../../contract/v2/fixtures/service-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var value project.ServiceContractDocument
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestVersionedServiceInterfaces 支持多个协议 / 版本，固定摘要独立于 JSON 对象键顺序。
func TestVersionedServiceInterfaces(t *testing.T) {
	catalog, err := New()
	if err != nil {
		t.Fatal(err)
	}
	document := serviceDocument(t)
	_, reference, err := catalog.ValidateServiceDocument(document)
	if err != nil || reference.ID != document.ID || reference.Version != document.Version {
		t.Fatalf("自定义多接口契约不合法: %+v %v", reference, err)
	}
	_, same, err := catalog.ValidateServiceDocument(document)
	if err != nil || same != reference {
		t.Fatal("相同规范化内容的摘要不稳定")
	}
	document.ConfigurationSchema["description"] = "明确的新内容"
	_, changed, err := catalog.ValidateServiceDocument(document)
	if err != nil || changed.Digest == reference.Digest {
		t.Fatal("内容变化未反映到不可变摘要")
	}
}

// TestServiceSchemaKeywordLocations 业务字段名、示例和实际 schema 引用的位置不能混淆。
func TestServiceSchemaKeywordLocations(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"$ref": map[string]any{"type": "string"}, "$schema": map[string]any{"type": "string"}}, "examples": []any{map[string]any{"$ref": "literal-business-value"}}}
	if err := validateInlineSchema(schema); err != nil {
		t.Fatalf("把业务字段误当 schema 关键字: %v", err)
	}
	local := map[string]any{"$defs": map[string]any{"Value": map[string]any{"type": "string"}}, "type": "object", "properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/Value"}}}
	if err := validateInlineSchema(local); err != nil {
		t.Fatalf("合法内部引用被拒绝: %v", err)
	}
	for _, reference := range []string{"https://example.invalid/schema", "file:///outside", "../other.json"} {
		local["properties"] = map[string]any{"value": map[string]any{"$ref": reference}}
		if err := validateInlineSchema(local); err == nil {
			t.Fatal("接受了包外引用")
		}
	}
}

// TestServiceSchemaCompositions 对组合、元组与未求值项同样执行片段引用边界。
func TestServiceSchemaCompositions(t *testing.T) {
	valid := map[string]any{"$defs": map[string]any{"Value": map[string]any{"type": "string"}}, "allOf": []any{true, map[string]any{"type": "array", "prefixItems": []any{map[string]any{"$ref": "#/$defs/Value"}}, "unevaluatedItems": false}}}
	if err := validateInlineSchema(valid); err != nil {
		t.Fatalf("合法本地组合被拒绝: %v", err)
	}
	for _, key := range []string{"allOf", "prefixItems", "unevaluatedItems"} {
		reference := map[string]any{"$ref": "https://service.invalid/owned"}
		var child any = reference
		if key != "unevaluatedItems" {
			child = []any{reference}
		}
		if err := validateInlineSchema(map[string]any{"$id": "https://service.invalid/owned", key: child}); err == nil {
			t.Fatalf("组合 %s 绕过仅内部片段的边界", key)
		}
	}
}

// TestInvalidServiceDocuments 实际 schema 和端口包不完整时拒绝，不降级为无检查的默认端口。
func TestInvalidServiceDocuments(t *testing.T) {
	catalog, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*project.ServiceContractDocument){
		func(value *project.ServiceContractDocument) { value.ID = "../outside" },
		func(value *project.ServiceContractDocument) { value.Version = "latest" },
		func(value *project.ServiceContractDocument) { value.ConfigurationSchema = nil },
		func(value *project.ServiceContractDocument) {
			value.ConfigurationSchema = map[string]any{"type": "not-a-type"}
		},
		func(value *project.ServiceContractDocument) {
			value.Interfaces = append(value.Interfaces, value.Interfaces[0])
		},
		func(value *project.ServiceContractDocument) { value.Ports = append(value.Ports, value.Ports[0]) },
		func(value *project.ServiceContractDocument) { value.Ports[0].Role = "both" },
		func(value *project.ServiceContractDocument) { value.Ports[0].Cardinality = "unlimited" },
		func(value *project.ServiceContractDocument) { value.Ports[0].Protocol = "MySQL" },
		func(value *project.ServiceContractDocument) { value.Ports[1].Contract = "unknown@1.0.0" },
		func(value *project.ServiceContractDocument) { value.Ports[1].Required = true },
		func(value *project.ServiceContractDocument) {
			value.ConfigurationSchema["description"] = strings.Repeat("x", 32768)
		},
	} {
		value := serviceDocument(t)
		change(&value)
		if _, _, err := catalog.ValidateServiceDocument(value); err == nil {
			t.Fatalf("接受了不完整契约: %+v", value.Ports)
		}
	}
}

// TestServiceSchemaBoundsAndExternalFile 真实外部文件也不能补全契约，过深材料明确拒绝。
func TestServiceSchemaBoundsAndExternalFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(file, []byte(`{"type":"object"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateInlineSchema(map[string]any{"$ref": "file://" + file}); err == nil {
		t.Fatal("读取了包外真实文件")
	}
	root := map[string]any{}
	current := root
	for range 22 {
		nested := map[string]any{}
		current["additionalProperties"] = nested
		current = nested
	}
	if err := validateInlineSchema(root); err == nil {
		t.Fatal("未限制 schema 深度")
	}
	if err := validateInlineSchema(map[string]any{"$schema": "http://json-schema.org/draft-07/schema#"}); err == nil {
		t.Fatal("未限制方言")
	}
}
