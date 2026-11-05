// 本文件用共享样本验证真实 JSON Schema、默认规范化和离线引用边界。
package catalog

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

//go:embed definitions/configuration-cases.json
var configurationCases []byte

// configurationCase 是前后端共享样本，不另写隐含校验预期。
type configurationCase struct {
	Name          string
	Configuration map[string]any
	Valid         bool
}

// TestConfigurationCases 与前端读取同一文件，防止两端字段和边界逐渐分叉。
func TestConfigurationCases(t *testing.T) {
	catalog, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var cases []configurationCase
	if err := json.Unmarshal(configurationCases, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) { validateConfigurationCase(t, catalog, test) })
	}
}

// validateConfigurationCase 同时核实真实校验、服务端缺省值和原输入保留。
func validateConfigurationCase(t *testing.T, catalog *Catalog, test configurationCase) {
	t.Helper()
	before, err := json.Marshal(test.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	value, reasons, err := catalog.Normalize(catalog.List().Definitions[0].Reference, test.Configuration)
	if (err == nil) != test.Valid {
		t.Fatalf("校验与共享样本不同: %v", err)
	}
	if test.Valid && (value["charset"] != "utf8mb4" || value["collation"] != "utf8mb4_0900_ai_ci" || reasons == nil) {
		t.Fatal("缺省值或未就绪集合未规范化")
	}
	after, err := json.Marshal(test.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("规范化改写了原始候选")
	}
}

// TestDefinitionIdentityAndReadiness 缺运行配置仍是合法草稿，未知定义不能借用默认值。
func TestDefinitionIdentityAndReadiness(t *testing.T) {
	catalog, err := New()
	if err != nil {
		t.Fatal(err)
	}
	definition := catalog.List().Definitions[0]
	if definition.Reference.Digest != catalog.List().Digest || !strings.Contains(definition.Image, "@sha256:") || definition.Ports[0].ID != "mysql" {
		t.Fatal("定义不完整或版本未固定")
	}
	value, reasons, err := catalog.Normalize(definition.Reference, map[string]any{})
	if err != nil || len(reasons) != 1 || !reflect.DeepEqual(value["memoryMiB"], float64(512)) {
		t.Fatalf("合法未就绪草稿被拒绝: %v %v", reasons, err)
	}
	_, reasons, err = catalog.Normalize(definition.Reference, map[string]any{"runtimeProfile": "local-compose-v1"})
	if err != nil || len(reasons) != 0 {
		t.Fatal("完整配置未就绪原因错误")
	}
	for _, field := range []string{"id", "version", "digest"} {
		reference := definition.Reference
		switch field {
		case "id":
			reference.ID = "other"
		case "version":
			reference.Version = "9.0.0"
		case "digest":
			reference.Digest = "sha256:" + strings.Repeat("0", 64)
		}
		if _, _, err := catalog.Normalize(reference, map[string]any{}); err == nil {
			t.Fatal("接受了未知定义引用")
		}
	}
	if _, _, err := catalog.Normalize(definition.Reference, nil); err == nil {
		t.Fatal("空配置容器未拒绝")
	}
}

// TestExternalSchemaReferenceRejected 即使外部文件真实有效，也不能从目录外解析它。
func TestExternalSchemaReferenceRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"$ref": "file://" + path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compileConfiguration(data); err == nil || !strings.Contains(err.Error(), "禁止") {
		t.Fatalf("目录外引用未阻止: %v", err)
	}
}
