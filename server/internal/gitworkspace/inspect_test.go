// 本文件通过真实文件系统和 Git 检查路径边界、空仓库与只读保证。
package gitworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// gitFixture 只在测试拥有的临时工作区执行准备命令。
func gitFixture(t *testing.T, path string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("准备 Git: %v %s", err, output)
	}
}

// fingerprint 记录内容与权限，导入前后必须一致，不以调用次数代替只读验证。
func fingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		result[path] = hex.EncodeToString(hash[:]) + info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestReadOnlyRepositoryInspection 验证普通、子目录和符号链接归一。
func TestReadOnlyRepositoryInspection(t *testing.T) {
	t.Run("登记真实仓库不改写源文件", inspectReadOnlyScenario)
}

// TestEmptyRepositoryAndPaths 显式区分空仓库、非仓库、不可读和越界。
func TestEmptyRepositoryAndPaths(t *testing.T) {
	t.Run("空仓库和非法路径", inspectPathScenario)
}

// TestLinkedWorktreeBoundary 防止只检查可见工作区而忽略外置 Git 元数据。
func TestLinkedWorktreeBoundary(t *testing.T) {
	t.Run("链接工作区元数据必须同样允许", func(t *testing.T) {
		outer := t.TempDir()
		source := filepath.Join(outer, "source")
		allowed := filepath.Join(outer, "allowed")
		for _, path := range []string{source, allowed} {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		}
		gitFixture(t, source, "init", "-b", "develop")
		gitFixture(t, source, "-c", "user.name=测试", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "夹具")
		worktree := filepath.Join(allowed, "service")
		gitFixture(t, source, "worktree", "add", "-b", "other", worktree)
		inspector, err := New([]string{allowed})
		if err != nil {
			t.Fatal(err)
		}
		_, err = inspector.Inspect(context.Background(), worktree)
		var failure *project.Error
		if !errors.As(err, &failure) || failure.Code != "GIT_METADATA_NOT_ALLOWED" {
			t.Fatalf("外置元数据越界未拒绝: %v", err)
		}
		inspector, err = New([]string{outer})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = inspector.Inspect(context.Background(), worktree); err != nil {
			t.Fatal(err)
		}
	})
}

// inspectReadOnlyScenario 执行中文登记与恢复场景。
func inspectReadOnlyScenario(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "service")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "init", "-b", "develop")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("用户的服务规范\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "add", "AGENTS.md")
	gitFixture(t, repo, "-c", "user.name=测试", "-c", "user.email=test@example.invalid", "commit", "-m", "测试夹具")
	before := fingerprint(t, repo)
	inspector, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", "/does-not-exist")
	reference := inspectRepository(t, inspector, repo)
	if reference.Path != repo || reference.HeadCommit == nil {
		t.Fatalf("错误的规范化结果: %+v", reference)
	}
	for _, path := range []string{filepath.Join(repo, "src"), link} {
		value := inspectRepository(t, inspector, path)
		if !reflect.DeepEqual(reference, value) {
			t.Fatal("同一仓库未归一")
		}
	}
	if !reflect.DeepEqual(before, fingerprint(t, repo)) {
		t.Fatal("导入改写了仓库内容、索引或配置")
	}
}

// inspectPathScenario 执行中文登记与恢复场景。
func inspectPathScenario(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "empty")
	plain := filepath.Join(root, "plain")
	unreadable := filepath.Join(root, "unreadable")
	for _, path := range []string{repo, plain, unreadable} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	gitFixture(t, repo, "init", "-b", "develop")
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadable, 0755); err != nil {
			t.Error(err)
		}
	})
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	inspector, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	value, err := inspector.Inspect(context.Background(), repo)
	if err != nil || value.HeadCommit != nil {
		t.Fatalf("空仓库不能伪造提交: %+v %v", value, err)
	}
	for _, test := range []struct{ path, code string }{{"relative", "INVALID_PATH"}, {plain, "NOT_GIT_WORKTREE"}, {filepath.Join(root, "missing"), "PATH_UNREADABLE"}, {unreadable, "PATH_UNREADABLE"}, {outside, "PATH_NOT_ALLOWED"}, {link, "PATH_NOT_ALLOWED"}} {
		t.Run(test.code+filepath.Base(test.path), func(t *testing.T) {
			_, err := inspector.Inspect(context.Background(), test.path)
			assertFailure(t, err, test.code)
		})
	}
}

// inspectRepository 让同一仓库的多个入口共享真实检查与失败诊断。
func inspectRepository(t *testing.T, inspector *Inspector, path string) project.RepositorySource {
	t.Helper()
	value, err := inspector.Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// assertFailure 保持路径场景的稳定错误契约断言，失败不被吞掉。
func assertFailure(t *testing.T, err error, code string) {
	t.Helper()
	var failure *project.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("期望 %s，得到 %v", code, err)
	}
}
