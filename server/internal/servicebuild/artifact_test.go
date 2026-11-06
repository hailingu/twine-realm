// 本文件验证真实文件和镜像元数据的边界，不能用tag或不安全文件替代内容证明。
package servicebuild

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// TestActualOutputFileBoundaries 允许受控普通文件并收紧权限，拒绝链接、目录、空和超限输出。
func TestActualOutputFileBoundaries(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "output")
	if err := os.WriteFile(file, []byte("real output"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := producedFile(file, 32)
	if err != nil || string(data) != "real output" {
		t.Fatal("真实普通输出无法读取", err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("输出权限未保持私有")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, link, filepath.Join(root, "missing")} {
		if _, err := producedFile(path, 32); err == nil {
			t.Fatal("不安全输出被读取")
		}
	}
	if _, err := producedFile(file, 2); err == nil {
		t.Fatal("超限输出被读取")
	}
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := producedFile(file, 32); err == nil {
		t.Fatal("空输出被接受")
	}
	if err := os.WriteFile(file, []byte("not ELF"), 0600); err != nil {
		t.Fatal(err)
	}
	assertBuildFault(t, func() error { _, err := verifyBinary(file, "amd64", 128); return err }(), "BUILD_BINARY_INVALID")
}

// TestActualImageMetadata 核实真实manifest/config两个摘要，任何警告、缺字段或tag都不能通过。
func TestActualImageMetadata(t *testing.T) {
	manifest, config := ContentDigest([]byte("manifest")), ContentDigest([]byte("config"))
	valid := map[string]any{"containerimage.digest": manifest, "containerimage.config.digest": config, "buildx.build.warnings": []any{}}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	actualManifest, actualConfig, err := imageMetadata(data)
	if err != nil || actualManifest != manifest || actualConfig != config || actualManifest == actualConfig {
		t.Fatal("不同内容摘要未分别保留")
	}
	for _, raw := range []string{"invalid", "{}", `{"containerimage.digest":"tag"}`, `{"containerimage.digest":"` + manifest + `","containerimage.config.digest":"` + config + `","buildx.build.warnings":[{"message":"real warning"}]}`} {
		if _, _, err := imageMetadata([]byte(raw)); err == nil {
			t.Fatal("不完整镜像证明被接受")
		}
	}
	data, err = json.Marshal(map[string]any{"containerimage.digest": manifest, "containerimage.config.digest": config, "buildx.build.warnings": map[string]any{"warning": "real warning"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := imageMetadata(data); err == nil {
		t.Fatal("非空告警对象被当成无告警")
	}
	if _, err := decodeJSONForTest([]byte(`{"id":"x","extra":true}`)); err == nil {
		t.Fatal("未知字段被接受")
	}
}

// decodeJSONForTest 从私有解析边界验证未知字段与尾随对象均拒绝。
func decodeJSONForTest(data []byte) (value struct {
	ID string `json:"id"`
}, err error) {
	err = decode(data, &value)
	return value, err
}

// TestSafeDiagnosticsAndCompletions 包装错误和工具输出不会泄漏进公共错误或伪造成功。
func TestSafeDiagnosticsAndCompletions(t *testing.T) {
	values := []struct {
		err  error
		code string
	}{
		{failure("BUILD_CHECK_FAILED", "受控失败"), "BUILD_CHECK_FAILED"},
		{&localdocker.Fault{Code: "DOCKER_RESULT_UNKNOWN", Message: "固定诊断", Unknown: true, Output: []byte("synthetic-private-value")}, "DOCKER_RESULT_UNKNOWN"},
		{&privatefiles.Error{Code: "FILE_UNSAFE", Message: "固定诊断"}, "BUILD_FILE_UNSAFE"},
		{&engineering.Error{Code: "ENGINEERING_DOCUMENT_CHANGED", Message: "固定诊断"}, "ENGINEERING_DOCUMENT_CHANGED"},
		{project.Failure("SOURCE_UNAVAILABLE", "固定诊断"), "SOURCE_UNAVAILABLE"},
		{errors.New("synthetic-private-value"), "BUILD_RESULT_UNKNOWN"},
	}
	for _, value := range values {
		code, message, _ := diagnostic(errors.Join(value.err, errors.New("outer")))
		if code != value.code || strings.Contains(message, "synthetic-private-value") {
			t.Fatal("诊断分类或隔离失效")
		}
		if domainError(value.err).Error() != message {
			t.Fatal("公共错误暴露不同内容")
		}
		completion := buildCompletion(nil, value.err, false)
		if completion.State == "succeeded" || completion.Candidate != nil {
			t.Fatal("错误构建被误报成功")
		}
	}
	_, value := validMetadata(t)
	if buildCompletion(value.Candidate, nil, false).State != "succeeded" || buildCompletion(value.Candidate, nil, true).State != "needs_attention" {
		t.Fatal("成功和中断被混淆")
	}
	if buildCompletion(nil, nil, false).State != "needs_attention" {
		t.Fatal("空结果冒充成功")
	}
}
