// 本文件验证实际环境解析与受限文件，不将密码材料写入失败诊断。
package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestConfigurationRoles 常驻应用和迁移只读取各自用途身份，不共用管理密码。
func TestConfigurationRoles(t *testing.T) {
	values := map[string]string{"DB_HOST": "127.0.0.1", "DB_NAME": "service_test", "DB_USER": "application", "DB_PASSWORD_FILE": "/secrets/app", "MIGRATION_USER": "migrator", "MIGRATION_PASSWORD_FILE": "/secrets/migration"}
	get := func(key string) string { return values[key] }
	app, err := Load(get, false)
	if err != nil || app.Database.User != "application" || app.Address != "127.0.0.1:8080" || app.Database.Port != "3306" {
		t.Fatalf("应用配置不合法：%v", err)
	}
	migration, err := Load(get, true)
	if err != nil || migration.Database.User != "migrator" || migration.Database.PasswordFile != "/secrets/migration" {
		t.Fatalf("迁移身份混用：%v", err)
	}
	for field, invalid := range map[string]string{"HTTP_ADDRESS": ":8080", "DB_NAME": "../other", "DB_PORT": "0", "DB_HOST": "host?password=value", "DB_USER": "root@other", "DB_PASSWORD_FILE": ""} {
		copy := map[string]string{}
		for key, value := range values {
			copy[key] = value
		}
		copy[field] = invalid
		if _, err := Load(func(key string) string { return copy[key] }, false); err == nil {
			t.Fatal("接受非法配置")
		}
	}
}

// TestPasswordFiles 明确拒绝宽松权限、符号链接、超长和多行材料。
func TestPasswordFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "password")
	secret := strings.Repeat("x", 32)
	if err := os.WriteFile(file, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := ReadPassword(file)
	if err != nil || value != secret {
		t.Fatal("受限文件未正确读取")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPassword(file); err == nil {
		t.Fatal("接受宽松权限")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPassword(link); err == nil {
		t.Fatal("读取符号链接")
	}
	for _, data := range []string{"short", strings.Repeat("a", 4097), secret + "\nextra"} {
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPassword(file); err == nil || strings.Contains(err.Error(), data) {
			t.Fatal("未安全拒绝非法密码文件")
		}
	}
	if _, err := ReadPassword("relative"); err == nil {
		t.Fatal("接受隐含相对密码路径")
	}
}

// TestPasswordFIFO 不允许命名管道让配置读取永久等待外部写入者。
func TestPasswordFIFO(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(file, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadPassword(file); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("接受命名管道")
		}
	case <-time.After(300 * time.Millisecond):
		writer, err := os.OpenFile(file, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		<-done
		t.Fatal("密码读取被命名管道阻塞")
	}
}
