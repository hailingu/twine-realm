// 本文件验证真实部分克隆缺失对象时不能隐式拉取，更不能改写原Git对象库。
package gitworkspace

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// partialCloneFixture 用本次拥有的file://远端建立缺blob克隆，不访问外部托管服务。
func partialCloneFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	seed, origin, clone := filepath.Join(root, "seed"), filepath.Join(root, "origin.git"), filepath.Join(root, "partial")
	if err := os.Mkdir(seed, 0700); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "init", "--bare", "-b", "develop", origin)
	gitFixture(t, origin, "config", "uploadpack.allowFilter", "true")
	gitFixture(t, seed, "init", "-b", "develop")
	writeSource(t, seed, "source.txt", "fixed source bytes\n")
	gitFixture(t, seed, "add", ".")
	gitFixture(t, seed, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	remote := (&url.URL{Scheme: "file", Path: origin}).String()
	gitFixture(t, seed, "push", remote, "HEAD:develop")
	gitFixture(t, root, "clone", "--filter=blob:none", "--no-checkout", remote, clone)
	return clone, gitValue(t, seed, "rev-parse", "HEAD"), gitValue(t, seed, "rev-parse", "HEAD:source.txt")
}

// TestMissingPromisorObjectsRemainReadOnly 对象读取和导出均明确失败，整个原仓库内容与权限不变。
func TestMissingPromisorObjectsRemainReadOnly(t *testing.T) {
	for _, mode := range []string{"object", "export"} {
		t.Run(mode, func(t *testing.T) {
			root, commit, blob := partialCloneFixture(t)
			before := fingerprint(t, root)
			inspector, err := New([]string{root})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "object" {
				_, err = inspector.command(context.Background(), root, "cat-file", "blob", blob)
			} else {
				_, err = inspector.ExportCommit(context.Background(), root, commit, filepath.Join(t.TempDir(), "export"))
			}
			if err == nil {
				t.Fatal("缺失对象被隐式补拉并用于读取")
			}
			if !reflect.DeepEqual(before, fingerprint(t, root)) {
				t.Fatal("只读访问改变了源仓库对象/配置/索引或引用")
			}
		})
	}
}

// TestOldGitCannotSilentlyIgnoreReadOnlyPolicy 只认识旧参数的Git须在装配时明确拒绝，不能靠未知环境变量假装安全。
func TestOldGitCannotSilentlyIgnoreReadOnlyPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "git"), []byte("#!/bin/sh\nexit 129\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	_, err := New([]string{root})
	assertFailure(t, err, "GIT_UNAVAILABLE")
}
