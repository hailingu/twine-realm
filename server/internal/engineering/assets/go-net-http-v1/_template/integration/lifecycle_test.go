//go:build integration

// 本文件经过实际用途账号、迁移锁、HTTP 子进程及 SIGTERM 验证最小支持组合。
package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"example.invalid/service/internal/migrations"
	"example.invalid/service/internal/schema"
)

// TestMigrationReadinessAndRestart 真实应用从未就绪转为就绪，重复迁移和重启不重置数据。
func TestMigrationReadinessAndRestart(t *testing.T) {
	f := newFixture(t)
	server := f.start(t)
	server.request(t, "/livez", 200)
	server.request(t, "/readyz", 503)
	if output, err := f.execute(t, "migrate"); err != nil {
		t.Fatalf("真实迁移未完成：%s", output)
	}
	server.request(t, "/readyz", 200)
	before := server.request(t, "/api/info", 200)
	if output, err := f.execute(t, "migrate"); err != nil {
		t.Fatalf("重复迁移失败：%s", output)
	}
	if after := server.request(t, "/api/info", 200); string(after) != string(before) {
		t.Fatal("重复迁移重置了持久初始化身份")
	}
	status, err := f.execute(t, "migration-status")
	if err != nil {
		t.Fatal("迁移状态查询失败")
	}
	var state schema.State
	if json.Unmarshal(status, &state) != nil || state.Version == nil || state.Dirty || state.Integrity != "verified" {
		t.Fatal("实际迁移证据不完整")
	}
	if err := server.stop(); err != nil {
		t.Fatal(err)
	}
	restarted := f.start(t)
	restarted.request(t, "/readyz", 200)
	if after := restarted.request(t, "/api/info", 200); string(after) != string(before) {
		t.Fatal("进程重启丢失持久初始化身份")
	}
	application := f.application(t)
	for _, statement := range []string{"CREATE TABLE forbidden(id INT)", "UPDATE schema_migrations SET dirty=0", "SELECT * FROM mysql.user"} {
		if _, err := application.Exec(statement); err == nil {
			t.Fatal("应用身份获得了迁移或管理权限")
		}
	}
}

// TestDirtyAndEvidenceRemainBlocked 真实异常历史不能靠再次运行命令清除或改写。
func TestDirtyAndEvidenceRemainBlocked(t *testing.T) {
	for _, scenario := range []string{"dirty", "改摘要", "缺摘要"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.execute(t, "migrate"); err != nil {
				t.Fatal("初始化迁移失败")
			}
			corruptHistory(t, f, scenario)
			if _, err := f.execute(t, "migrate"); err == nil {
				t.Fatal("异常历史被自动放行")
			}
			server := f.start(t)
			server.request(t, "/livez", 200)
			server.request(t, "/readyz", 503)
			requireBlockedHistory(t, f, scenario)
		})
	}
}

// requireBlockedHistory 直接读取原库证据，确认就绪仍受阻且 dirty 没有被隐式修复。
func requireBlockedHistory(t *testing.T, f *fixture, scenario string) {
	t.Helper()
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	state, err := schema.Inspect(context.Background(), f.application(t), set)
	if err != nil || state.Ready(set) {
		t.Fatal("异常迁移证据变成就绪")
	}
	if scenario == "dirty" && !state.Dirty {
		t.Fatal("dirty 标记被自动清除")
	}
}

// corruptHistory 仅改变本次库中的指定证据，避免测试主流程混合 SQL 夹具细节。
func corruptHistory(t *testing.T, f *fixture, scenario string) {
	t.Helper()
	statements := map[string]string{
		"dirty": "UPDATE " + f.name + ".schema_migrations SET dirty=1",
		"改摘要":   "UPDATE " + f.name + ".service_migration_digests SET digest=REPEAT('0',64)",
		"缺摘要":   "DELETE FROM " + f.name + ".service_migration_digests",
	}
	statement, exists := statements[scenario]
	if !exists {
		t.Fatal("异常历史场景未定义")
	}
	if _, err := f.admin.Exec(statement); err != nil {
		t.Fatal("异常历史夹具创建失败")
	}
}

// TestRestrictedMigrationAndCancellation 迁移权限不足或预先取消不能伪造已完成初始化。
func TestRestrictedMigrationAndCancellation(t *testing.T) {
	f := newFixture(t)
	configuration := f.configuration(false)
	if err := migrations.Apply(context.Background(), configuration); err == nil {
		t.Fatal("应用身份完成了 DDL")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := migrations.Apply(ctx, f.configuration(true)); err == nil {
		t.Fatal("已取消迁移仍报告成功")
	}
	server := f.start(t)
	server.request(t, "/readyz", 503)
	if data := server.request(t, "/livez", 200); !strings.Contains(string(data), "alive") {
		t.Fatal("依赖故障影响存活语义")
	}
}

// TestUnavailableDatabaseKeepsLiveness 即使 TCP 开放却无法完成 MySQL 认证，进程仍存活、依赖不就绪。
func TestUnavailableDatabaseKeepsLiveness(t *testing.T) {
	f := newFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.port = port
	server := f.start(t)
	server.request(t, "/livez", 200)
	server.request(t, "/readyz", 503)
	server.request(t, "/api/info", 503)
	server.request(t, "/livez", 200)
}
