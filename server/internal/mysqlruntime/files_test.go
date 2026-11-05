// 本文件穿过真实本机文件系统验证私有材料、FIFO 和已有数据保护。
package mysqlruntime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// openPrivateFixture 只创建并清理测试拥有的全新目录。
func openPrivateFixture(t *testing.T) (*Driver, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime")
	if err := InitializeDirectory(path, nil, testEnvironment()); err != nil {
		t.Fatal(err)
	}
	driver, err := OpenDriver(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	return driver, path
}

// testResource 使用随机归属，不把显示名或测试目录当资源身份。
func testResource(t *testing.T, driver *Driver) Resource {
	t.Helper()
	id, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return (Resource{InstallationID: driver.InstallationID(), ProjectID: id, ComponentID: id, DaemonID: driver.Environment().DaemonID, Image: Image}).WithID(id)
}

// TestPrivateRootBoundary 已存在、仓库内、权限改变或链接均不作为安全运行根。
func TestPrivateRootBoundary(t *testing.T) {
	driver, path := openPrivateFixture(t)
	before, err := os.ReadFile(filepath.Join(path, "installation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeDirectory(path, nil, testEnvironment()); err == nil {
		t.Fatal("覆盖了既有运行根")
	}
	after, err := os.ReadFile(filepath.Join(path, "installation.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("既有安装清单被改写")
	}
	if _, err := OpenDriver(path, []string{filepath.Dir(path)}); err == nil {
		t.Fatal("仓库内部的秘密目录被接受")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDriver(link, nil); err == nil {
		t.Fatal("符号链接运行根被接受")
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.readPrivate("installation.json", 4096); err == nil {
		t.Fatal("启动后不安全根权限仍被接受")
	}
}

// TestSecretsKeepDataIdentity 原秘密重复使用，缺失、改变与不安全材料不触发重置。
func TestSecretsKeepDataIdentity(t *testing.T) {
	driver, path := openPrivateFixture(t)
	resource := testResource(t, driver)
	if err := driver.prepareSecrets(resource, false); err != nil {
		t.Fatal(err)
	}
	name := "projects/" + resource.ID + "/root.secret"
	before, err := driver.readPrivate(name, 128)
	if err != nil || len(before) != 64 {
		t.Fatal("没有生成正确秘密文件")
	}
	if err := driver.prepareSecrets(resource, true); err != nil {
		t.Fatal(err)
	}
	after, err := driver.readPrivate(name, 128)
	if err != nil || string(after) != string(before) {
		t.Fatal("已有秘密被替换")
	}
	if err := os.Remove(filepath.Join(path, filepath.FromSlash(name))); err != nil {
		t.Fatal(err)
	}
	if err := driver.prepareSecrets(resource, true); err == nil {
		t.Fatal("已有数据卷却生成了新密码")
	}
	if _, err := driver.readPrivate(name, 128); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("缺失原秘密时产生了新文件")
	}
}

// TestPrivateSpecialFiles 特殊文件不能阻塞读取，链接和非0600文件明确拒绝。
func TestPrivateSpecialFiles(t *testing.T) {
	driver, path := openPrivateFixture(t)
	fifo := filepath.Join(path, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := driver.readPrivate("fifo", 128); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO被视为普通秘密文件")
		}
	case <-time.After(time.Second):
		file, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}
		t.Fatal("FIFO读取发生阻塞")
	}
	if err := os.Symlink(filepath.Join(path, "installation.json"), filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.readPrivate("link", 4096); err == nil {
		t.Fatal("秘密符号链接被接受")
	}
	if err := os.WriteFile(filepath.Join(path, "public"), []byte("not-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.readPrivate("public", 128); err == nil {
		t.Fatal("公开权限文件被接受")
	}
}
