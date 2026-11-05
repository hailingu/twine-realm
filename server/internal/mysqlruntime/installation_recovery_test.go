// 本文件验证安装清单与私有根的真实文件边界，失败绝不重新初始化原目录。
package mysqlruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// rejectedInstallation 统一核实失败打开的诊断，并释放任何意外返回的目录句柄。
func rejectedInstallation(t *testing.T, path, code string) {
	t.Helper()
	opened, err := OpenDriver(path, nil)
	if opened != nil {
		if err := opened.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("损坏或不安全的安装被打开")
	}
	privateFailure(t, err, code)
}

// changedInstallationBytes 构造外部改动后的完整字节，原始数据只保存在独占夹具内。
func changedInstallationBytes(t *testing.T, value installation, change func(*installation), suffix string) []byte {
	t.Helper()
	if change == nil {
		return []byte(suffix)
	}
	change(&value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, []byte(suffix)...)
}

// changePrivateSubdirectory 只改变夹具内的空目录，不接触产品或用户材料。
func changePrivateSubdirectory(t *testing.T, child, kind string) {
	t.Helper()
	if kind == "public" {
		if err := os.Chmod(child, 0755); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if kind == "symlink" {
		if err := os.Symlink(t.TempDir(), child); err != nil {
			t.Fatal(err)
		}
	}
}

// TestInvalidInstallationKeepsOriginalFile 清单损坏、额外内容或不受支持的身份均保留原字节。
func TestInvalidInstallationKeepsOriginalFile(t *testing.T) {
	cases := []struct {
		name   string
		change func(*installation)
		suffix string
	}{
		{"版本不受支持", func(v *installation) { v.SchemaVersion = 2 }, ""},
		{"适配器不受支持", func(v *installation) { v.Adapter = "unknown" }, ""},
		{"安装身份无效", func(v *installation) { v.ID = "invalid" }, ""},
		{"环境身份无效", func(v *installation) { v.Environment.DaemonID = "" }, ""},
		{"额外JSON", func(*installation) {}, "{}"},
		{"截断JSON", nil, "{"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			driver, path := openPrivateFixture(t)
			data := changedInstallationBytes(t, driver.installation, item.change, item.suffix)
			file := filepath.Join(path, "installation.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			rejectedInstallation(t, path, "RUNTIME_INSTALLATION_INVALID")
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(after, data) {
				t.Fatal("打开失败改写了原安装清单")
			}
		})
	}
}

// TestChangedPrivateSubdirectoryCannotReopen 子目录被移除、公开或链接替换后必须阻塞原安装。
func TestChangedPrivateSubdirectoryCannotReopen(t *testing.T) {
	for _, kind := range []string{"missing", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			_, path := openPrivateFixture(t)
			child := filepath.Join(path, "projects")
			changePrivateSubdirectory(t, child, kind)
			rejectedInstallation(t, path, "RUNTIME_FILE_UNSAFE")
		})
	}
}

// TestReplacedPrivateRootCannotUseStaleDescriptor 即使新旧目录权限相同也不把旧描述符作为新根。
func TestReplacedPrivateRootCannotUseStaleDescriptor(t *testing.T) {
	driver, path := openPrivateFixture(t)
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := driver.readPrivate("installation.json", 4096)
	privateFailure(t, err, "RUNTIME_DIRECTORY_UNSAFE")
	privateFailure(t, driver.writeNew("marker", []byte("must-not-write")), "RUNTIME_DIRECTORY_UNSAFE")
	for _, directory := range []string{path, path + "-original"} {
		if _, err := os.Stat(filepath.Join(directory, "marker")); !os.IsNotExist(err) {
			t.Fatal("目录身份改变后仍产生了文件")
		}
	}
}

// TestInitializationRejectsUnsafeLocations 无效新根、未准备父目录与仓库重叠不得产生新安装。
func TestInitializationRejectsUnsafeLocations(t *testing.T) {
	base := t.TempDir()
	for _, path := range []string{"relative", filepath.Join(base, "bad$root"), filepath.Join(base, "missing", "runtime")} {
		privateFailure(t, InitializeDirectory(path, nil, testEnvironment()), "RUNTIME_DIRECTORY_INVALID")
	}
	path := filepath.Join(base, "runtime")
	privateFailure(t, InitializeDirectory(path, []string{base}, testEnvironment()), "RUNTIME_DIRECTORY_UNSAFE")
	privateFailure(t, InitializeDirectory(path, []string{filepath.Join(path, "service")}, testEnvironment()), "RUNTIME_DIRECTORY_UNSAFE")
	environment := testEnvironment()
	environment.DaemonID = ""
	privateFailure(t, InitializeDirectory(path, nil, environment), "RUNTIME_ENVIRONMENT_INVALID")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("失败初始化创建了根目录")
	}
}
