//go:build integration

// 本文件在自有数据库逐项改变用途边界，证明被动核实拒绝漂移而不静默修复。
package mysqlruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bindingDriftCase 通过本测试拥有的管理会话改变一个事实，不开放生产任意SQL入口。
type bindingDriftCase struct {
	name, code string
	change     func(context.Context, *BindingSession) error
}

// bindingSQLChange 仅将本文件内的固定SQL作用于当前随机绑定，不根据响应继续执行命令。
func bindingSQLChange(query func(*BindingSession) string) func(context.Context, *BindingSession) error {
	return func(ctx context.Context, session *BindingSession) error {
		_, err := session.sql.query(ctx, query(session))
		return err
	}
}

// bindingDriftCases 覆盖库、账号、权限与私有材料；失败后不执行补偿DDL或密码重置。
func bindingDriftCases() []bindingDriftCase {
	return []bindingDriftCase{
		{"额外全局权限", "DEPLOY_PRIVILEGES_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "GRANT PROCESS ON *.* TO " + s.accountSQL(s.request.ApplicationUser)
		})},
		{"跨库权限", "DEPLOY_PRIVILEGES_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "GRANT SELECT ON mysql.* TO " + s.accountSQL(s.request.ApplicationUser)
		})},
		{"保护历史写入", "DEPLOY_PRIVILEGES_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "GRANT INSERT ON " + sqlIdentifier(s.request.Binding.DatabaseName) + ".schema_migrations TO " + s.accountSQL(s.request.ApplicationUser)
		})},
		{"应用权限缺失", "DEPLOY_PRIVILEGES_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "REVOKE UPDATE ON " + sqlIdentifier(s.request.Binding.DatabaseName) + ".todos FROM " + s.accountSQL(s.request.ApplicationUser)
		})},
		{"迁移表级权限", "DEPLOY_PRIVILEGES_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "GRANT SELECT ON " + sqlIdentifier(s.request.Binding.DatabaseName) + ".todos TO " + s.accountSQL(s.request.MigrationUser)
		})},
		{"迁移账号被解锁", "DEPLOY_ACCOUNT_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "ALTER USER " + s.accountSQL(s.request.MigrationUser) + " ACCOUNT UNLOCK"
		})},
		{"应用账号被锁定", "DEPLOY_ACCOUNT_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "ALTER USER " + s.accountSQL(s.request.ApplicationUser) + " ACCOUNT LOCK"
		})},
		{"额外账号Host", "DEPLOY_ACCOUNT_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "CREATE USER '" + s.request.ApplicationUser + "'@'localhost' ACCOUNT LOCK"
		})},
		{"账号归属被改写", "DEPLOY_ACCOUNT_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "ALTER USER " + s.accountSQL(s.request.ApplicationUser) + " ATTRIBUTE '{\"twineBinding\":\"foreign\"}'"
		})},
		{"业务身份丢失", "DEPLOY_DATABASE_IDENTITY_CONFLICT", bindingSQLChange(func(s *BindingSession) string {
			return "DROP TABLE " + sqlIdentifier(s.request.Binding.DatabaseName) + ".twine_binding_identity"
		})},
		{"秘密版本记录丢失", "DEPLOY_SECRET_INVALID", func(_ context.Context, s *BindingSession) error {
			return os.Remove(filepath.Join(s.driver.directory, s.prefix, "credentials.json"))
		}},
		{"秘密内容被替换", "DEPLOY_SECRET_INVALID", func(_ context.Context, s *BindingSession) error {
			return os.WriteFile(s.Files().Application, []byte("not-a-password"), 0600)
		}},
	}
}

// TestRealBindingDriftMatrix 被动采集必须检测应用/迁移用途漂移，不以非空权限或旧连接健康掩盖它。
func TestRealBindingDriftMatrix(t *testing.T) {
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	fact := executeRealGoal(t, driver, lease)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, test := range bindingDriftCases() {
		t.Run(test.name, func(t *testing.T) {
			request := realBindingRequest(t, driver, lease, fact)
			err := driver.WithBinding(ctx, request, func(session *BindingSession) error {
				return verifyBindingDrift(ctx, t, session, test)
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.code {
				t.Fatal("真实用途漂移未明确阻断", test.code, err)
			}
		})
	}
}

// verifyBindingDrift 只改变已经完成的本次用途绑定，失败不能由初始化错误冒充。
func verifyBindingDrift(ctx context.Context, t *testing.T, session *BindingSession, test bindingDriftCase) error {
	t.Helper()
	if err := session.Prepare(ctx); err != nil {
		return err
	}
	if err := seedBindingHistory(ctx, t, session); err != nil {
		return err
	}
	if err := session.ApplicationPermissions(ctx); err != nil {
		return err
	}
	if err := test.change(ctx, session); err != nil {
		t.Fatalf("测试漂移未建立：%v", err)
	}
	return session.Verify(ctx)
}
