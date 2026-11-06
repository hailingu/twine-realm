//go:build integration

// 本文件在门禁独立MySQL上核实业务库、用途账号、内容历史和秘密恢复边界。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// bindingID 为临时数据库和凭据分配真实随机身份，不触及用户对象。
func bindingID(t *testing.T) string {
	t.Helper()
	value, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// realBindingRequest 使用本次原运行实例的真实网段，不使用广泛Host通配符。
func realBindingRequest(t *testing.T, driver *Driver, lease Lease, fact *Observation) BindingRequest {
	t.Helper()
	info, err := driver.BindingDatabase(context.Background(), lease.Target.Resource)
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR(info.NetworkCIDR)
	if err != nil {
		t.Fatal(err)
	}
	serviceID, credentialID := bindingID(t), bindingID(t)
	suffix := strings.ReplaceAll(credentialID, "-", "")[:24]
	binding := project.DatabaseBinding{ID: bindingID(t), Source: project.PortEndpoint{ComponentID: serviceID}, Target: project.PortEndpoint{ComponentID: lease.Target.Resource.ComponentID}, DatabaseID: bindingID(t), DatabaseName: "todo_" + suffix, Purpose: "readWrite", Credential: project.CredentialReference{ID: credentialID, Version: "1"}}
	return BindingRequest{Resource: lease.Target.Resource, ServerUUID: fact.ServerUUID, ServiceID: serviceID, ApplicationUser: "tra_" + suffix, MigrationUser: "trm_" + suffix, AccountHost: network.IP.String() + "/" + net.IP(network.Mask).String(), Binding: binding}
}

// TestRealBindingProvisionAndHistory 验证真实权限与内容证据，脏迁移和丢失秘密不会被自动改写。
func TestRealBindingProvisionAndHistory(t *testing.T) {
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	fact := executeRealGoal(t, driver, lease)
	request := realBindingRequest(t, driver, lease, fact)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var password []byte
	err := driver.WithBinding(ctx, request, func(session *BindingSession) error {
		var err error
		password, err = prepareBindingWitness(ctx, t, session)
		return err
	})
	if err != nil {
		t.Fatal("真实业务绑定未通过", err)
	}
	err = driver.WithBinding(ctx, request, func(session *BindingSession) error { return verifyBindingWitness(ctx, t, session, password) })
	if err != nil {
		t.Fatal("真实业务权限或历史核实失败", err)
	}
	assertBindingSecretRecovery(t, ctx, driver, request)
}

// prepareBindingWitness 先验证新库为空，再仅在本测试中建立已应用内容见证。
func prepareBindingWitness(ctx context.Context, t *testing.T, session *BindingSession) ([]byte, error) {
	t.Helper()
	if err := session.Prepare(ctx); err != nil {
		return nil, err
	}
	password, err := os.ReadFile(session.Files().Application)
	if err != nil {
		return nil, err
	}
	if err := session.Prepare(ctx); err != nil {
		return nil, err
	}
	history, err := session.History(ctx, map[uint64]string{1: strings.Repeat("a", 64)})
	if err != nil {
		return nil, err
	}
	if history.Version != 0 || history.Integrity != "missing" {
		t.Error("新库被伪报为已完成迁移")
	}
	return password, seedBindingHistory(ctx, t, session)
}

// verifyBindingWitness 单独核实原秘密、真实授权与dirty保留，不混入初始化动作。
func verifyBindingWitness(ctx context.Context, t *testing.T, session *BindingSession, password []byte) error {
	t.Helper()
	if err := session.Prepare(ctx); err != nil {
		return err
	}
	if err := session.ApplicationPermissions(ctx); err != nil {
		return err
	}
	if err := session.Verify(ctx); err != nil {
		return err
	}
	after, err := os.ReadFile(session.Files().Application)
	if err != nil {
		return err
	}
	if !bytes.Equal(password, after) {
		t.Error("恢复改变了原业务秘密")
	}
	expected := map[uint64]string{1: strings.Repeat("a", 64)}
	history, err := session.History(ctx, expected)
	if err != nil {
		return err
	}
	if history.Version != 1 || history.Integrity != "verified" {
		t.Error("干净版本与真实摘要未通过核实")
	}
	assertBindingPrivilegeScopes(ctx, t, session)
	if _, err := session.sql.query(ctx, "UPDATE "+sqlIdentifier(session.request.Binding.DatabaseName)+".`schema_migrations` SET dirty=1"); err != nil {
		return err
	}
	history, err = session.History(ctx, expected)
	if err != nil {
		return err
	}
	if history.Integrity != "dirty" {
		t.Error("脏迁移被误判干净")
	}
	return nil
}

// seedBindingHistory 只在本测试全新业务库制造已应用见证，不将此管理写入冒称真实迁移任务。
func seedBindingHistory(ctx context.Context, t *testing.T, session *BindingSession) error {
	t.Helper()
	db := sqlIdentifier(session.request.Binding.DatabaseName)
	queries := []string{"CREATE TABLE " + db + ".`todos` (id INT PRIMARY KEY,title VARCHAR(100))", "CREATE TABLE " + db + ".`schema_migrations` (version BIGINT PRIMARY KEY,dirty BOOLEAN NOT NULL)", "INSERT INTO " + db + ".`schema_migrations` VALUES(1,0)", "CREATE TABLE " + db + ".`service_migration_digests` (version BIGINT PRIMARY KEY,digest CHAR(64) NOT NULL)", "INSERT INTO " + db + ".`service_migration_digests` VALUES(1,'" + strings.Repeat("a", 64) + "')"}
	for _, query := range queries {
		if _, err := session.sql.query(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// assertBindingPrivilegeScopes 查询真实授权表，应用不获库级DDL、全局权限或迁移历史写权限。
func assertBindingPrivilegeScopes(ctx context.Context, t *testing.T, session *BindingSession) {
	t.Helper()
	grantee := sqlText("'" + session.request.ApplicationUser + "'@'" + session.request.AccountHost + "'")
	queries := []string{"SELECT COUNT(*) FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE=" + grantee, "SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=" + grantee + " AND PRIVILEGE_TYPE<>'USAGE'", "SELECT COUNT(*) FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE=" + grantee + " AND TABLE_NAME IN ('schema_migrations','service_migration_digests','twine_binding_identity') AND PRIVILEGE_TYPE<>'SELECT'", "SELECT COUNT(*) FROM mysql.user WHERE User='" + session.request.MigrationUser + "' AND account_locked<>'Y'"}
	for _, query := range queries {
		assertSQLScalar(t, session.sql, query, "0")
	}
	assertSQLScalar(t, session.sql, "SELECT COUNT(*) FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE="+grantee+" AND TABLE_NAME='todos'", "4")
}

// assertBindingSecretRecovery 缺原秘密时阻断且不重置账号，也不清除之前的dirty见证。
func assertBindingSecretRecovery(t *testing.T, ctx context.Context, driver *Driver, request BindingRequest) {
	t.Helper()
	files, err := driver.BindingPaths(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files.Application); err != nil {
		t.Fatal(err)
	}
	err = driver.WithBinding(ctx, request, func(session *BindingSession) error { return session.Prepare(ctx) })
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "DEPLOY_SECRET_MISSING" {
		t.Fatal("丢失原秘密没有明确阻断", err)
	}
	if _, err := os.Stat(files.Application); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("恢复重新生成了丢失的原秘密")
	}
	err = driver.WithBinding(ctx, request, func(session *BindingSession) error {
		assertSQLScalar(t, session.sql, "SELECT dirty FROM "+sqlIdentifier(request.Binding.DatabaseName)+".`schema_migrations`", "1")
		assertSQLScalar(t, session.sql, "SELECT COUNT(*) FROM mysql.user WHERE User='"+request.ApplicationUser+"'", "1")
		return nil
	})
	if err != nil {
		t.Fatal("原数据或账号见证未保留", err)
	}
}
