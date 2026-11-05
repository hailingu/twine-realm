// 本文件用真实 Git 验证固定对象、独立观察和不执行仓库脚本的边界。
package gitworkspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sourceFixture 准备可重复提交的自有工作区，返回规范路径及只读适配器。
func sourceFixture(t *testing.T) (string, *Inspector) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "init", "-b", "develop")
	gitFixture(t, root, "config", "user.name", "测试")
	gitFixture(t, root, "config", "user.email", "test@example.invalid")
	writeSource(t, root, "source.txt", "已提交内容\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-m", "初始版本")
	inspector, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	return root, inspector
}

// writeSource 仅修改测试拥有的夹具文件，不经过平台适配器。
func writeSource(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

// gitValue 为夹具准备读取真实对象身份，外层 Git 定位不能污染自有仓库。
func gitValue(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// TestSourceObservationReadOnly 验证完整 HEAD、分支、修改、游离与固定值互不替代。
func TestSourceObservationReadOnly(t *testing.T) {
	root, inspector := sourceFixture(t)
	pinned := gitValue(t, root, "rev-parse", "HEAD")
	before := fingerprint(t, root)
	value, err := inspector.Observe(context.Background(), root, &pinned)
	if err != nil || value.HeadCommit == nil || *value.HeadCommit != pinned || value.Branch == nil || *value.Branch != "develop" || value.Dirty == nil || *value.Dirty || value.PinnedAvailable == nil || !*value.PinnedAvailable || value.ObservedAt == "" {
		t.Fatalf("不完整观察: %+v %v", value, err)
	}
	if err := inspector.VerifyCommit(context.Background(), root, pinned); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fingerprint(t, root)) {
		t.Fatal("观察或固定校验改写了仓库")
	}
	writeSource(t, root, "source.txt", "第二版本\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-m", "后续版本")
	gitFixture(t, root, "checkout", "--detach")
	writeSource(t, root, "untracked.txt", "未提交\n")
	before = fingerprint(t, root)
	t.Setenv("GIT_DIR", "/cannot-use-outer-git")
	value, err = inspector.Observe(context.Background(), root, &pinned)
	if err != nil || value.Branch != nil || value.HeadCommit == nil || *value.HeadCommit == pinned || value.Dirty == nil || !*value.Dirty || value.PinnedAvailable == nil || !*value.PinnedAvailable {
		t.Fatalf("HEAD 或修改覆盖固定值: %+v %v", value, err)
	}
	if !reflect.DeepEqual(before, fingerprint(t, root)) {
		t.Fatal("脏工作区观察写入索引或文件")
	}
}

// TestOnlyCompleteCommitObjects 拒绝缩写、缺失对象以及可解析的其他对象类型。
func TestOnlyCompleteCommitObjects(t *testing.T) {
	root, inspector := sourceFixture(t)
	gitFixture(t, root, "tag", "-a", "v1", "-m", "标签对象")
	for _, test := range []struct{ ref, code string }{{"HEAD^{tree}", "INVALID_COMMIT_OBJECT"}, {"HEAD:source.txt", "INVALID_COMMIT_OBJECT"}, {"refs/tags/v1", "INVALID_COMMIT_OBJECT"}} {
		assertFailure(t, inspector.VerifyCommit(context.Background(), root, gitValue(t, root, "rev-parse", test.ref)), test.code)
	}
	for _, value := range []string{"HEAD", "deadbee", strings.Repeat("A", 40)} {
		assertFailure(t, inspector.VerifyCommit(context.Background(), root, value), "INVALID_COMMIT")
	}
	missing := strings.Repeat("f", 40)
	assertFailure(t, inspector.VerifyCommit(context.Background(), root, missing), "COMMIT_NOT_FOUND")
	value, err := inspector.Observe(context.Background(), root, &missing)
	if err != nil || value.PinnedAvailable == nil || *value.PinnedAvailable || value.Warning == nil {
		t.Fatalf("缺失固定对象未明确展示: %+v %v", value, err)
	}
}

// TestUnbornSourceHasNoInventedCommit 空仓库可观察，但没有可供固定的 HEAD。
func TestUnbornSourceHasNoInventedCommit(t *testing.T) {
	root, inspector := sourceFixture(t)
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, empty, "init", "-b", "new")
	value, err := inspector.Observe(context.Background(), empty, nil)
	if err != nil || !value.Available || value.HeadCommit != nil || value.Branch == nil || *value.Branch != "new" || value.PinnedAvailable != nil {
		t.Fatalf("空仓库伪造状态: %+v %v", value, err)
	}
}

// TestConfiguredScriptsNeverRun 拒绝为 dirty 观察执行用户配置的过滤器或子模块。
func TestConfiguredScriptsNeverRun(t *testing.T) {
	root, inspector := sourceFixture(t)
	marker := filepath.Join(root, "executed")
	gitFixture(t, root, "config", "filter.danger.clean", "touch "+marker)
	writeSource(t, root, ".gitattributes", "source.txt filter=danger\n")
	writeSource(t, root, "source.txt", "必须触发检查的修改\n")
	value, err := inspector.Observe(context.Background(), root, nil)
	if err != nil || value.Dirty != nil || value.Warning == nil {
		t.Fatalf("未保持未知修改状态: %+v %v", value, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("执行了用户过滤器: %v", err)
	}
	gitFixture(t, root, "config", "--remove-section", "filter.danger")
	head := gitValue(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",nested")
	value, err = inspector.Observe(context.Background(), root, nil)
	if err != nil || value.Dirty != nil || value.Warning == nil {
		t.Fatalf("子模块应返回明确未知: %+v %v", value, err)
	}
}

// TestSharedObjectsStayWithinAllowedRoots 校验共享对象库及间接配置链接不会越界。
func TestSharedObjectsStayWithinAllowedRoots(t *testing.T) {
	root, inspector := sourceFixture(t)
	outside := t.TempDir()
	file := filepath.Join(root, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(file, []byte(outside+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := inspector.Observe(context.Background(), root, nil)
	assertFailure(t, err, "GIT_METADATA_NOT_ALLOWED")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(outside, "alternates")
	if err := os.WriteFile(external, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, file); err != nil {
		t.Fatal(err)
	}
	_, err = inspector.Observe(context.Background(), root, nil)
	assertFailure(t, err, "GIT_METADATA_NOT_ALLOWED")
}

// TestBoundedGitOutput 返回明确错误，禁止静默截断为可信观察。
func TestBoundedGitOutput(t *testing.T) {
	var output gitOutput
	if _, err := output.Write(make([]byte, 512*1024)); err != nil {
		t.Fatal(err)
	}
	if count, err := output.Write([]byte("x")); count != 0 || err == nil {
		t.Fatal("未限制 Git 输出")
	}
}
