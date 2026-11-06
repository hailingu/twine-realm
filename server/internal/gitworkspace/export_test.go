// 本文件穿过真实Git对象、属性和文件系统验证固定commit导出及原仓库不变。
package gitworkspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestExportUsesRawPinnedCommit 工作区漂移、过滤器和archive属性不改变实际导出的固定字节。
func TestExportUsesRawPinnedCommit(t *testing.T) {
	root, inspector := sourceFixture(t)
	writeSource(t, root, ".gitattributes", "source.txt filter=blocked\nraw.txt export-subst\nhidden.txt export-ignore\n")
	writeSource(t, root, "raw.txt", "$Format:%H$\n")
	writeSource(t, root, "hidden.txt", "must-stay\n")
	writeSource(t, root, "binary", "\x00\n raw \n\x00")
	if err := os.WriteFile(filepath.Join(root, "executable"), []byte("not-executed\n"), 0755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-m", "固定原始材料")
	pinned := gitValue(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "config", "filter.blocked.clean", "false")
	gitFixture(t, root, "config", "filter.blocked.required", "true")
	writeSource(t, root, "source.txt", "dirty-not-included\n")
	writeSource(t, root, "untracked", "not-included")
	before := fingerprint(t, root)
	t.Setenv("GIT_DIR", "/must-not-use-outer-state")
	destination := filepath.Join(t.TempDir(), "export")
	value, err := inspector.ExportCommit(context.Background(), root, pinned, destination)
	if err != nil || value.Commit != pinned || value.Digest == "" || len(value.Files) != 6 {
		t.Fatalf("固定材料导出未完成: %v", err)
	}
	for name, expected := range map[string]string{"source.txt": "已提交内容\n", "raw.txt": "$Format:%H$\n", "hidden.txt": "must-stay\n", "binary": "\x00\n raw \n\x00"} {
		data, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || !bytes.Equal(data, []byte(expected)) {
			t.Fatal("导出替换了原始字节、应用了属性或混入工作区")
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "untracked")); !os.IsNotExist(err) {
		t.Fatal("未提交文件进入了源码副本")
	}
	info, err := os.Stat(filepath.Join(destination, "executable"))
	if err != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatal("提交中的执行权限丢失")
	}
	repeated, err := inspector.ExportCommit(context.Background(), root, pinned, filepath.Join(t.TempDir(), "export"))
	if err != nil || !reflect.DeepEqual(value, repeated) || !reflect.DeepEqual(before, fingerprint(t, root)) {
		t.Fatal("固定材料不确定，或导出改写了原仓库/索引/分支/配置")
	}
}

// TestExportRejectsLinksAndGitlinks 不解析链接或子模块，也不先创建不完整目标。
func TestExportRejectsLinksAndGitlinks(t *testing.T) {
	for _, kind := range []string{"symlink", "gitlink"} {
		t.Run(kind, func(t *testing.T) {
			root, inspector := sourceFixture(t)
			if kind == "symlink" {
				if err := os.Symlink("source.txt", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
				gitFixture(t, root, "add", ".")
			} else {
				commit := gitValue(t, root, "rev-parse", "HEAD")
				gitFixture(t, root, "update-index", "--add", "--cacheinfo", "160000,"+commit+",module")
			}
			gitFixture(t, root, "commit", "-m", "不支持的对象")
			destination := filepath.Join(t.TempDir(), "export")
			_, err := inspector.ExportCommit(context.Background(), root, gitValue(t, root, "rev-parse", "HEAD"), destination)
			assertFailure(t, err, "SOURCE_KIND_UNSUPPORTED")
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("不支持的对象产生了部分导出目录")
			}
		})
	}
}

// TestExportProtectsOriginalAndExistingDestinations 目标不能在原仓库内，也不能覆盖原有目录。
func TestExportProtectsOriginalAndExistingDestinations(t *testing.T) {
	root, inspector := sourceFixture(t)
	pinned := gitValue(t, root, "rev-parse", "HEAD")
	before := fingerprint(t, root)
	_, err := inspector.ExportCommit(context.Background(), root, pinned, filepath.Join(root, "copy"))
	assertFailure(t, err, "SOURCE_EXPORT_BOUNDARY")
	destination := t.TempDir()
	writeSource(t, destination, "marker", "keep")
	_, err = inspector.ExportCommit(context.Background(), root, pinned, destination)
	assertFailure(t, err, "SOURCE_EXPORT_EXISTS")
	data, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(data) != "keep" || !reflect.DeepEqual(before, fingerprint(t, root)) {
		t.Fatal("失败导出改变了原仓库或已有目标")
	}
}
