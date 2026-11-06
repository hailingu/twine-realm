//go:build integration

// 本文件穿过真实Docker交互与MySQL，证明锁属于原连接且关闭后才释放。
package mysqlruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// realSQLSession 为本次已认证资源建立通道，断言提前失败也回收连接。
func realSQLSession(t *testing.T, driver *Driver, resource Resource, containerID string) *sqlSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	session, err := driver.databaseSQL(ctx, resource, containerID)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		cancel()
	})
	return session
}

// assertSQLScalar 不输出原始响应，失败时只指出当前受控见证未成立。
func assertSQLScalar(t *testing.T, session *sqlSession, sql, expected string) {
	t.Helper()
	rows, err := session.query(context.Background(), sql)
	if err != nil || len(rows) != 1 || rows[0] != expected {
		t.Fatal("真实数据库交互见证不匹配", err)
	}
}

// TestRealSQLSessionLockOwnership 同一会话持锁时第二连接不能取得，原连接关闭后才可接续。
func TestRealSQLSessionLockOwnership(t *testing.T) {
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	fact := executeRealGoal(t, driver, lease)
	if fact == nil || !fact.Ready {
		t.Fatal("真实MySQL未认证就绪")
	}
	id, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	lock := "twine-session-" + id
	first := realSQLSession(t, driver, lease.Target.Resource, fact.ContainerID)
	second := realSQLSession(t, driver, lease.Target.Resource, fact.ContainerID)
	assertSQLScalar(t, first, "SELECT GET_LOCK('"+lock+"',0)", "1")
	assertSQLScalar(t, first, "SELECT 1", "1")
	assertSQLScalar(t, second, "SELECT GET_LOCK('"+lock+"',0)", "0")
	if err := first.Close(); err != nil {
		t.Fatal("原连接未确认关闭", err)
	}
	assertSQLScalar(t, second, "SELECT GET_LOCK('"+lock+"',0)", "1")
	assertSQLScalar(t, second, "SELECT RELEASE_LOCK('"+lock+"')", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	foreign, err := driver.databaseSQL(ctx, lease.Target.Resource, strings.Repeat("f", 64))
	var fault *Fault
	if foreign != nil || !errors.As(err, &fault) || fault.Code != "RUNTIME_OWNERSHIP_CONFLICT" {
		t.Fatal("管理会话接受了未绑定的容器身份")
	}
}

// TestRealSQLPhaseCancellation 真正阻塞的MySQL交互受阶段期限约束，未知通道不能补发后续请求。
func TestRealSQLPhaseCancellation(t *testing.T) {
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	fact := executeRealGoal(t, driver, lease)
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelParent()
	session, err := driver.databaseSQL(parent, lease.Target.Resource, fact.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(parent, 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = session.query(ctx, "SELECT SLEEP(5)")
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "DATABASE_RESULT_UNKNOWN" || !fault.Unknown || time.Since(started) > 2*time.Second {
		t.Error("真实阶段取消未中断受控客户端或丢失未知结果", err)
	}
	if _, err := session.query(parent, "CREATE DATABASE forbidden_after_cancel"); err == nil {
		t.Error("取消后未知通道接受了新写入")
	}
	if session.Close() == nil {
		t.Error("关闭错误地隐藏原未知结果")
	}
}
