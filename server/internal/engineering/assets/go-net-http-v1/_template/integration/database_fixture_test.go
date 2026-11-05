//go:build integration

// 本文件只创建自有随机库与用途账号；管理凭据不传给服务或迁移子进程。
package integration_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.invalid/service/internal/config"
	"example.invalid/service/internal/database"
	"github.com/go-sql-driver/mysql"
)

// fixture 拥有本次库、账号、密码文件和测试环境，失败时只清理已确认创建的对象。
type fixture struct {
	admin             *sql.DB
	name              string
	appUser           string
	migrationUser     string
	appPassword       string
	migrationPassword string
	directory         string
	host              string
	port              string
	users             []string
	ownedDatabase     bool
}

// token 只产生适合固定 SQL 标识和密码的随机十六进制，不包含外部输入。
func token(t *testing.T, size int) string {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal("随机测试身份不可用")
	}
	return hex.EncodeToString(data)
}

// newFixture 必需隔离数据库缺失时明确失败，不把缺少环境当跳过。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	settings, err := mysql.ParseDSN(os.Getenv("TWINE_TEMPLATE_ADMIN_DSN"))
	if err != nil || settings.User == "" {
		t.Fatal("模板集成检查需要自有临时数据库管理配置")
	}
	host, port, err := net.SplitHostPort(settings.Addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("模板集成数据库必须使用本机回环地址")
	}
	settings.InterpolateParams = true
	settings.ParseTime = true
	settings.Loc = time.UTC
	admin, err := sql.Open("mysql", settings.FormatDSN())
	if err != nil {
		t.Fatal("模板测试管理连接不可用")
	}
	f := &fixture{admin: admin, name: "profile_" + token(t, 8), appUser: "app_" + token(t, 8), migrationUser: "migration_" + token(t, 8), appPassword: token(t, 24), migrationPassword: token(t, 24), directory: t.TempDir(), host: host, port: port}
	t.Cleanup(func() { f.cleanup(t) })
	f.initialize(t)
	return f
}

// initialize 分别记录已创建的库与账号，失败不会把同名外部对象误认为自有。
func (f *fixture) initialize(t *testing.T) {
	t.Helper()
	if _, err := f.admin.Exec("CREATE DATABASE " + f.name + " CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal("自有模板测试库创建失败")
	}
	f.ownedDatabase = true
	for _, account := range []struct{ user, password string }{{f.appUser, f.appPassword}, {f.migrationUser, f.migrationPassword}} {
		if _, err := f.admin.Exec("CREATE USER '"+account.user+"'@'%' IDENTIFIED BY ?", account.password); err != nil {
			t.Fatal("自有模板用途账号创建失败")
		}
		f.users = append(f.users, account.user)
	}
	if _, err := f.admin.Exec("GRANT SELECT ON " + f.name + ".* TO '" + f.appUser + "'@'%'"); err != nil {
		t.Fatal("测试应用最小权限配置失败")
	}
	if _, err := f.admin.Exec("GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,DROP,INDEX,REFERENCES ON " + f.name + ".* TO '" + f.migrationUser + "'@'%'"); err != nil {
		t.Fatal("测试迁移权限配置失败")
	}
	for name, password := range map[string]string{"app.secret": f.appPassword, "migration.secret": f.migrationPassword} {
		if err := os.WriteFile(filepath.Join(f.directory, name), []byte(password), 0600); err != nil {
			t.Fatal("测试密码文件创建失败")
		}
	}
}

// cleanup 不删未成功创建的对象，连接与数据清理失败保留为测试失败。
func (f *fixture) cleanup(t *testing.T) {
	t.Helper()
	for _, user := range f.users {
		if _, err := f.admin.Exec("DROP USER '" + user + "'@'%'"); err != nil {
			t.Error("自有模板测试账号未清理")
		}
	}
	if f.ownedDatabase {
		if _, err := f.admin.Exec("DROP DATABASE " + f.name); err != nil {
			t.Error("自有模板测试数据库未清理")
		}
	}
	if err := f.admin.Close(); err != nil {
		t.Error("模板测试管理连接未关闭")
	}
}

// configuration 根据模式给出独立身份，不把管理配置转换成应用配置。
func (f *fixture) configuration(migration bool) config.Database {
	value := config.Database{Host: f.host, Port: f.port, Name: f.name, User: f.appUser, PasswordFile: filepath.Join(f.directory, "app.secret")}
	if migration {
		value.User = f.migrationUser
		value.PasswordFile = filepath.Join(f.directory, "migration.secret")
	}
	return value
}

// application 取得真实最小权限连接，并明确登记关闭责任。
func (f *fixture) application(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(f.configuration(false), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error("应用测试连接未关闭")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("测试应用身份无法认证")
	}
	return db
}
