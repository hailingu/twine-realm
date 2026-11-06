// 本文件破坏真实私有文件，验证登记后的用途秘密绝不被替换或放宽权限。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// privateBindingFixture 只准备本次文件材料，不伪装已创建数据库或账号。
func privateBindingFixture(t *testing.T) (*BindingSession, string) {
	t.Helper()
	driver, root := openPrivateFixture(t)
	resource := testResource(t, driver)
	if err := driver.prepareSecrets(resource, false); err != nil {
		t.Fatal(err)
	}
	request := BindingRequest{Resource: resource, ServiceID: resource.ProjectID, ServerUUID: resource.ComponentID, Binding: project.DatabaseBinding{ID: resource.ID, DatabaseID: resource.ID, DatabaseName: "private_test", Purpose: "readWrite"}}
	prefix := "projects/" + resource.ID + "/services/" + request.ServiceID + "/"
	session := &BindingSession{driver: driver, request: request, prefix: prefix}
	if err := session.bindingSecrets(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return session, filepath.Join(root, filepath.FromSlash(prefix))
}

// TestBindingPrivateFilesRejectDamage 已登记文件丢失、损坏、链接或权限改变均阻塞，原文件不被重建。
func TestBindingPrivateFilesRejectDamage(t *testing.T) {
	for _, mode := range []string{"missing", "invalid-hex", "short", "oversized", "public", "symlink", "record"} {
		t.Run(mode, func(t *testing.T) { assertBindingPrivateDamage(t, mode) })
	}
}

// assertBindingPrivateDamage 逐项改变一个真实文件，并核实失败后磁盘仍保持原现场。
func assertBindingPrivateDamage(t *testing.T, mode string) {
	t.Helper()
	session, directory := privateBindingFixture(t)
	file := filepath.Join(directory, "app.secret")
	if mode == "record" {
		file = filepath.Join(directory, "credentials.json")
	}
	changeBindingPrivateFile(t, file, mode)
	before, beforeErr := os.ReadFile(file)
	err := session.bindingSecrets(context.Background(), true)
	if err == nil {
		t.Fatal("损坏或不安全秘密被接受")
	}
	after, afterErr := os.ReadFile(file)
	if !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
		t.Fatal("失败后改写或补建了原秘密")
	}
	if mode == "public" {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0644 {
			t.Fatal("偷偷修复了原秘密权限", err)
		}
	}
}

// changeBindingPrivateFile 的所有操作仅作用于t.TempDir拥有的全新材料。
func changeBindingPrivateFile(t *testing.T, file, mode string) {
	t.Helper()
	var err error
	switch mode {
	case "missing":
		err = os.Remove(file)
	case "public":
		err = os.Chmod(file, 0644)
	case "symlink":
		if err = os.Remove(file); err == nil {
			err = os.Symlink(filepath.Join(filepath.Dir(file), "migration.secret"), file)
		}
	default:
		contents := map[string]string{"invalid-hex": strings.Repeat("z", 64), "short": "ab", "oversized": strings.Repeat("a", 129), "record": "{}"}
		err = os.WriteFile(file, []byte(contents[mode]), 0600)
	}
	if err != nil {
		t.Fatal("本次私有文件故障未注入", err)
	}
}

// TestBindingPrivateCancellationAndMissingParent 取消与缺失目录不能创建新秘密或覆盖登记。
func TestBindingPrivateCancellationAndMissingParent(t *testing.T) {
	session, directory := privateBindingFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, action := range []func() error{
		func() error { return session.bindingDirectory(ctx, false) },
		func() error { _, err := session.bindingPassword(ctx, "app.secret", false); return err },
		func() error { return session.bindingSecretRecord(ctx, nil, false) },
	} {
		if err := action(); !errors.Is(err, context.Canceled) {
			t.Fatal("取消未在文件动作前生效", err)
		}
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := session.bindingSecrets(context.Background(), true); err == nil {
		t.Fatal("缺失原目录被补建")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("缺失目录出现新身份", err)
	}
}
