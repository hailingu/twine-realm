//go:build integration

// 本文件验证非法用途、原数据身份和部分登记在实际MySQL前后都不能被接管或补造成功。
package mysqlruntime

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// bindingBoundaryDriver 只初始化本测试的原实例，其他测试或用户对象不作为前置。
func bindingBoundaryDriver(t *testing.T) (*Driver, Lease, *Observation) {
	t.Helper()
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	return driver, lease, executeRealGoal(t, driver, lease)
}

// TestRealBindingValidationBeforeEffects 不合法标识、用途或原身份不能进入管理动作。
func TestRealBindingValidationBeforeEffects(t *testing.T) {
	driver, lease, fact := bindingBoundaryDriver(t)
	original := realBindingRequest(t, driver, lease, fact)
	for _, test := range []struct {
		name, code string
		change     func(*BindingRequest)
	}{
		{"未知用途", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.Binding.Purpose = "admin" }},
		{"系统库", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.Binding.DatabaseName = "MySQL" }},
		{"非法库名", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.Binding.DatabaseName = "bad-name" }},
		{"应用身份越界", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.ApplicationUser = "root" }},
		{"迁移身份越界", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.MigrationUser = r.ApplicationUser }},
		{"凭据版本非法", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.Binding.Credential.Version = "bad" }},
		{"服务引用越界", "DEPLOY_BINDING_INVALID", func(r *BindingRequest) { r.Binding.Source.ComponentID = bindingID(t) }},
		{"数据UUID改变", "RUNTIME_DATABASE_IDENTITY_CONFLICT", func(r *BindingRequest) { r.ServerUUID = bindingID(t) }},
		{"私网改变", "DEPLOY_NETWORK_CHANGED", func(r *BindingRequest) { r.AccountHost = "192.0.2.0/255.255.255.0" }},
	} {
		t.Run(test.name, func(t *testing.T) { assertInvalidBinding(t, driver, original, test.change, test.code) })
	}
	assertBindingMaterialLimits(t, driver, original)
}

// assertInvalidBinding 回调只表达是否获得管理权，不用调用次数替代错误和归属断言。
func assertInvalidBinding(t *testing.T, driver *Driver, original BindingRequest, change func(*BindingRequest), code string) {
	t.Helper()
	request := original
	change(&request)
	called := false
	err := driver.WithBinding(context.Background(), request, func(*BindingSession) error { called = true; return nil })
	var fault *Fault
	if called || !errors.As(err, &fault) || fault.Code != code {
		t.Fatal("非法绑定取得了管理动作或诊断错误", code, err)
	}
	if code == "DEPLOY_BINDING_INVALID" {
		if _, err := driver.BindingPaths(request); err == nil {
			t.Fatal("非法绑定仍解析了用途路径")
		}
	}
}

// assertBindingMaterialLimits 取消和超限材料不产生文件，管理连接仍由原会话回收。
func assertBindingMaterialLimits(t *testing.T, driver *Driver, request BindingRequest) {
	t.Helper()
	err := driver.WithBinding(context.Background(), request, func(session *BindingSession) error {
		for _, data := range [][]byte{nil, make([]byte, 32769)} {
			if session.Material(context.Background(), data) == nil {
				t.Error("超限材料未拒绝")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(session.Material(ctx, []byte("{}")), context.Canceled) {
			t.Error("取消后仍写入运行材料")
		}
		if _, err := os.Stat(session.Files().Application); !errors.Is(err, os.ErrNotExist) {
			t.Error("拒绝材料时创建了业务秘密")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRealBindingRegistryBoundaries 同名已有库、未知建库和被改写登记都必须保留现场并阻塞。
func TestRealBindingRegistryBoundaries(t *testing.T) {
	driver, lease, fact := bindingBoundaryDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	request := realBindingRequest(t, driver, lease, fact)
	assertPreexistingBusinessDatabase(t, ctx, driver, request)
	for _, test := range []struct{ name, assignment, code string }{
		{"建库登记中断", "stage='intent'", "DEPLOY_DATABASE_CREATE_UNKNOWN"},
		{"未知登记阶段", "stage='foreign'", "DEPLOY_REGISTRY_CONFLICT"},
		{"登记摘要改变", "intent_digest='sha256:" + strings.Repeat("f", 64) + "'", "DEPLOY_REGISTRY_CONFLICT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := realBindingRequest(t, driver, lease, fact)
			err := driver.WithBinding(ctx, request, func(session *BindingSession) error { return changeBindingRegistry(ctx, session, test.assignment) })
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.code {
				t.Fatal("部分登记被自动接管或修复", test.code, err)
			}
		})
	}
	assertUnexpectedRegistrySchema(t, ctx, driver, realBindingRequest(t, driver, lease, fact))
}

// changeBindingRegistry 只修改本绑定已经存在的登记，重新准备不得把外部改写覆盖回去。
func changeBindingRegistry(ctx context.Context, session *BindingSession, assignment string) error {
	if err := session.Prepare(ctx); err != nil {
		return err
	}
	if _, err := session.sql.query(ctx, "UPDATE "+registryTable+" SET "+assignment+" WHERE binding_id="+sqlText(session.request.Binding.ID)); err != nil {
		return err
	}
	return session.Prepare(ctx)
}

// assertPreexistingBusinessDatabase 没有原登记的已有库保持未接管，平台不往其中补身份表。
func assertPreexistingBusinessDatabase(t *testing.T, ctx context.Context, driver *Driver, request BindingRequest) {
	t.Helper()
	err := driver.WithBinding(ctx, request, func(session *BindingSession) error {
		if _, err := session.sql.query(ctx, "CREATE DATABASE "+sqlIdentifier(request.Binding.DatabaseName)); err != nil {
			return err
		}
		return session.Prepare(ctx)
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "DEPLOY_DATABASE_OWNERSHIP_CONFLICT" {
		t.Fatal("同名已有库被接管", err)
	}
	err = driver.WithBinding(ctx, request, func(session *BindingSession) error {
		assertSQLScalar(t, session.sql, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA="+sqlText(request.Binding.DatabaseName), "0")
		assertSQLScalar(t, session.sql, "SELECT COUNT(*) FROM "+registryTable+" WHERE binding_id="+sqlText(request.Binding.ID), "0")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// assertUnexpectedRegistrySchema 不通过补建/改表修复外来登记结构，测试结束仅清理自有实例。
func assertUnexpectedRegistrySchema(t *testing.T, ctx context.Context, driver *Driver, request BindingRequest) {
	t.Helper()
	err := driver.WithBinding(ctx, request, func(session *BindingSession) error {
		if _, err := session.sql.query(ctx, "ALTER TABLE "+registryTable+" ADD COLUMN unexpected INT"); err != nil {
			return err
		}
		return session.Prepare(ctx)
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "DEPLOY_REGISTRY_CONFLICT" {
		t.Fatal("被改写的登记结构仍可执行", err)
	}
}
