// 本文件通过真实父目录别名验证未来仓库范围，不依赖 macOS 固有的 /var 链接。
package mysqlruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFutureForbiddenDirectoryFollowsExistingParent 未创建的仓库子目录仍能约束其真实上层运行根。
func TestFutureForbiddenDirectoryFollowsExistingParent(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, "runtime")
	future := filepath.Join(alias, "runtime", "services", "new-service")
	privateFailure(t, InitializeDirectory(path, []string{future}, testEnvironment()), "RUNTIME_DIRECTORY_UNSAFE")
	if _, err := os.Stat(filepath.Join(real, "runtime")); !os.IsNotExist(err) {
		t.Fatal("通过父目录别名在未来仓库上层创建了秘密根")
	}
	if err := InitializeDirectory(path, nil, testEnvironment()); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenDriver(path, []string{future})
	if opened != nil {
		if err := opened.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("重新打开通过别名绕过未来仓库范围")
	}
	privateFailure(t, err, "RUNTIME_DIRECTORY_UNSAFE")
}

// TestUnrelatedFutureDirectoryRemainsUsable 无关未来路径允许初始化，无法解析的链接则保持阻塞。
func TestUnrelatedFutureDirectoryRemainsUsable(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "runtime")
	forbidden := []string{filepath.Join(base, "future", "service")}
	if err := InitializeDirectory(path, forbidden, testEnvironment()); err != nil {
		t.Fatal(err)
	}
	driver, err := OpenDriver(path, forbidden)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "broken")
	if err := os.Symlink(filepath.Join(base, "missing"), link); err != nil {
		t.Fatal(err)
	}
	_, err = OpenDriver(path, []string{filepath.Join(link, "future")})
	privateFailure(t, err, "RUNTIME_DIRECTORY_INVALID")
}
