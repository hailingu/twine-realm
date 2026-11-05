// 本文件用实际导出、Git 检出和文件破坏验证离线规范与边界，不模拟已读。
package engineering

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// exported 为每个场景建立独立全新服务目录，不复用上一个测试的规范内容。
func exported(t *testing.T) (*Catalog, string) {
	t.Helper()
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "service")
	if _, err := catalog.Export(directory); err != nil {
		t.Fatal(err)
	}
	return catalog, directory
}

// TestExportAndCleanCheckout 验证独立本地 Git 检出仍可读取完整相对规范，不调用网络。
func TestExportAndCleanCheckout(t *testing.T) {
	catalog, directory := exported(t)
	git(t, directory, "init", "-b", "develop")
	git(t, directory, "add", ".")
	git(t, directory, "-c", "user.name=工程模板测试", "-c", "user.email=test@example.invalid", "commit", "-m", "模板离线快照")
	checkout := filepath.Join(t.TempDir(), "checkout")
	git(t, directory, "clone", "--no-hardlinks", directory, checkout)
	result, err := catalog.Validate(checkout)
	if err != nil || result.State != "documents-verified" || result.Profile != catalog.Identity() || len(result.Documents) != 4 {
		t.Fatalf("干净检出未能核实完整规范：%+v %v", result, err)
	}
	if result.Template != catalog.Profile().Template || !strings.HasPrefix(result.Template.Digest, "sha256:") {
		t.Fatal("未记录可追溯的原始模板摘要")
	}
	value := catalog.Profile()
	value.Documents[0].Path = "changed"
	value.Stack.Dependencies[0].Version = "other"
	value.Runtime.Commands[0][0] = "unsafe"
	if _, err := catalog.Validate(checkout); err != nil || catalog.Profile().Runtime.Commands[0][0] != "service" {
		t.Fatal("公开副本改变了目录真相")
	}
	before, err := os.ReadFile(filepath.Join(directory, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Export(directory); err == nil {
		t.Fatal("覆盖了既有服务目录")
	}
	after, err := os.ReadFile(filepath.Join(directory, "AGENTS.md"))
	if err != nil || string(after) != string(before) {
		t.Fatal("拒绝覆盖时改变了原内容")
	}
}

// git 只操作本次目录，并移除外层提交 hook 的 Git 定位，防止误写平台索引。
func git(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			command.Env = append(command.Env, value)
		}
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("测试 Git 操作失败：%v %s", err, output)
	}
}

// TestBlockedMaterial 破坏实际快照或绑定后必须得到明确阻塞，不回退其他版本。
func TestBlockedMaterial(t *testing.T) {
	for _, scenario := range []string{"缺文档", "改文档", "改方案", "改锁", "Go版本", "缺依赖", "缺引用", "重复引用", "越界", "符号链接"} {
		t.Run(scenario, func(t *testing.T) {
			catalog, directory := exported(t)
			document := filepath.Join(directory, "docs/engineering/general.md")
			switch scenario {
			case "缺文档":
				must(t, os.Remove(document))
			case "改文档":
				must(t, os.WriteFile(document, []byte("不得回显的外部材料"), 0600))
			case "改方案":
				must(t, os.WriteFile(filepath.Join(directory, ".twine/profile.json"), []byte(`{}`), 0600))
			case "改锁":
				must(t, os.WriteFile(filepath.Join(directory, ".twine/engineering.lock.json"), []byte(`{"schemaVersion":1,"profile":{"id":"other","version":"9.0.0","digest":"unknown"}}`), 0600))
			case "Go版本":
				replaceFile(t, filepath.Join(directory, "go.mod"), "go 1.27.3", "go 1.28.0")
			case "缺依赖":
				replaceFile(t, filepath.Join(directory, "go.mod"), "v1.10.1", "v1.9.0")
			case "缺引用":
				replaceFile(t, filepath.Join(directory, "AGENTS.md"), "```twine-engineering", "```other")
			case "重复引用":
				data, err := os.ReadFile(filepath.Join(directory, "AGENTS.md"))
				must(t, err)
				must(t, os.WriteFile(filepath.Join(directory, "AGENTS.md"), append(data, data...), 0600))
			case "越界":
				replaceFile(t, filepath.Join(directory, "AGENTS.md"), `"docs/engineering/general.md"`, `"../outside"`)
			case "符号链接":
				outside := filepath.Join(t.TempDir(), "private")
				must(t, os.WriteFile(outside, []byte("不得回显的外部材料"), 0600))
				must(t, os.Remove(document))
				must(t, os.Symlink(outside, document))
			}
			_, err := catalog.Validate(directory)
			var failure *Error
			if !errors.As(err, &failure) || strings.Contains(failure.Message, "不得回显") {
				t.Fatalf("未安全阻塞异常材料：%v", err)
			}
		})
	}
}

// TestNamedPipeCannotBlockValidation 即使显式目录中有 FIFO，普通核实也必须有界拒绝。
func TestNamedPipeCannotBlockValidation(t *testing.T) {
	catalog, directory := exported(t)
	name := filepath.Join(directory, "go.sum")
	must(t, os.Remove(name))
	must(t, syscall.Mkfifo(name, 0600))
	done := make(chan error, 1)
	go func() { _, err := catalog.Validate(directory); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("把 FIFO 当成普通材料")
		}
	case <-time.After(300 * time.Millisecond):
		writer, err := os.OpenFile(name, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		must(t, writer.Close())
		<-done
		t.Fatal("非普通文件使工程核实阻塞")
	}
}

// TestReleaseAndDecoderRejectCorruption 检查目录边界与严格清单解码，不放行未知或尾随字段。
func TestReleaseAndDecoderRejectCorruption(t *testing.T) {
	catalog, _ := exported(t)
	for _, descriptor := range []releaseFile{{Path: "../outside", Source: "AGENTS.md"}, {Path: "file", Source: "../outside"}, {Path: "file", Source: "AGENTS.md", Size: 70000}, {Path: "file", Source: "AGENTS.md", Size: 1, Digest: "changed"}} {
		candidate := *catalog
		candidate.release.Files = []releaseFile{descriptor}
		if candidate.verifyRelease() == nil {
			t.Fatal("接受损坏发布材料")
		}
	}
	for _, data := range []string{`{"unknown":1}`, `{} {}`, `{invalid`} {
		var value lock
		if decode([]byte(data), &value) == nil {
			t.Fatal("宽松解码未知清单")
		}
	}
	data, err := json.Marshal(catalog.Profile())
	must(t, err)
	var value Profile
	must(t, decode(data, &value))
}

// replaceFile 保留其他候选内容，仅改变指定测试语义。
func replaceFile(t *testing.T, path, before, after string) {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(data), before) {
		t.Fatal("测试材料缺少预期入口")
	}
	must(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(data), before, after)), 0600))
}

// must 不忽略临时文件操作的失败，防止负例没有实际建立。
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
