// 本文件只授予数据库用途权限，业务进程不能写迁移历史、执行DDL或管理账号。
package mysqlruntime

import (
	"context"
	"slices"
	"strings"
)

var migrationPrivileges = []string{"ALTER", "CREATE", "DELETE", "DROP", "INDEX", "INSERT", "REFERENCES", "SELECT", "UPDATE"}
var applicationPrivileges = []string{"DELETE", "INSERT", "SELECT", "UPDATE"}

const alterAccount = "ALTER USER "

// UnlockMigration 在原容器尚未执行时授予本库迁移权限；结束后必须重新锁定账号。
func (session *BindingSession) UnlockMigration(ctx context.Context) error {
	if err := session.verifyPrivileges(ctx, false); err != nil {
		return err
	}
	database, err := session.grantDatabase(ctx)
	if err != nil {
		return err
	}
	if _, err := session.sql.query(ctx, "GRANT "+strings.Join(migrationPrivileges, ",")+" ON "+database+".* TO "+session.accountSQL(session.request.MigrationUser)); err != nil {
		return err
	}
	_, err = session.sql.query(ctx, alterAccount+session.accountSQL(session.request.MigrationUser)+" ACCOUNT UNLOCK")
	return err
}

// LockMigration 不撤销迁移证据、不清dirty，仅停止后续用途连接。
func (session *BindingSession) LockMigration(ctx context.Context) error {
	_, err := session.sql.query(ctx, alterAccount+session.accountSQL(session.request.MigrationUser)+" ACCOUNT LOCK")
	return err
}

// ApplicationPermissions 对现有每张业务表授权DML，迁移元数据仅SELECT，不授予库级或全局权限。
func (session *BindingSession) ApplicationPermissions(ctx context.Context) error {
	if err := session.LockMigration(ctx); err != nil {
		return err
	}
	if err := session.verifyPrivileges(ctx, false); err != nil {
		return err
	}
	rows, err := session.sql.query(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA="+sqlText(session.request.Binding.DatabaseName)+" AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME")
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows) > 128 {
		return failure("DEPLOY_TABLES_UNSUPPORTED", "迁移未产生受支持的有界业务表集合", false, false)
	}
	for _, table := range rows {
		if !businessDatabase.MatchString(table) {
			return failure("DEPLOY_TABLES_UNSUPPORTED", "首个运行组合仅支持受限业务表标识符", false, false)
		}
		privileges := session.applicationTablePrivileges(table)
		query := "GRANT " + strings.Join(privileges, ",") + " ON " + sqlIdentifier(session.request.Binding.DatabaseName) + "." + sqlIdentifier(table) + " TO " + session.accountSQL(session.request.ApplicationUser)
		if _, err := session.sql.query(ctx, query); err != nil {
			return err
		}
	}
	if _, err := session.sql.query(ctx, alterAccount+session.accountSQL(session.request.ApplicationUser)+" ACCOUNT UNLOCK"); err != nil {
		return err
	}
	return session.verifyPrivileges(ctx, true)
}

// grantDatabase 根据实际partial_revokes语义转义数据库级通配符，不扩大下划线库名的授权范围。
func (session *BindingSession) grantDatabase(ctx context.Context) (string, error) {
	rows, err := session.sql.query(ctx, "SELECT @@GLOBAL.partial_revokes")
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || (rows[0] != "0" && rows[0] != "1") {
		return "", failure("DEPLOY_PRIVILEGES_UNKNOWN", "无法核实数据库授权语义", false, true)
	}
	name := session.request.Binding.DatabaseName
	if rows[0] == "0" {
		name = strings.ReplaceAll(name, "_", `\_`)
	}
	return sqlIdentifier(name), nil
}

// verifyPrivileges 拒绝额外全局、跨库、角色、列或例程权限，不通过静默REVOKE修复外部漂移。
func (session *BindingSession) verifyPrivileges(ctx context.Context, requireApplication bool) error {
	for _, user := range []string{session.request.ApplicationUser, session.request.MigrationUser} {
		grantee := sqlText("'" + user + "'@'" + session.request.AccountHost + "'")
		queries := []string{
			"SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=" + grantee + " AND (PRIVILEGE_TYPE<>'USAGE' OR IS_GRANTABLE<>'NO')",
			"SELECT COUNT(*) FROM mysql.global_grants WHERE USER='" + user + "' AND HOST='" + session.request.AccountHost + "'",
			"SELECT COUNT(*) FROM mysql.role_edges WHERE TO_USER='" + user + "' OR FROM_USER='" + user + "'",
			"SELECT COUNT(*) FROM information_schema.COLUMN_PRIVILEGES WHERE GRANTEE=" + grantee,
			"SELECT COUNT(*) FROM mysql.procs_priv WHERE User='" + user + "' AND Host='" + session.request.AccountHost + "'",
		}
		for _, query := range queries {
			rows, err := session.sql.query(ctx, query)
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0] != "0" {
				return failure("DEPLOY_PRIVILEGES_CONFLICT", "用途账号出现额外权限，须核实后恢复最小权限", false, false)
			}
		}
		if err := session.verifyScopedPrivileges(ctx, user, grantee, requireApplication); err != nil {
			return err
		}
	}
	if requireApplication {
		return session.verifyAccountAvailability(ctx)
	}
	return nil
}

// verifyAccountAvailability 已部署应用必须可连接、迁移身份必须锁定，旧连接健康不能掩盖用途漂移。
func (session *BindingSession) verifyAccountAvailability(ctx context.Context) error {
	for user, expected := range map[string]string{session.request.ApplicationUser: "N", session.request.MigrationUser: "Y"} {
		rows, err := session.sql.query(ctx, "SELECT account_locked FROM mysql.user WHERE User='"+user+"' AND Host='"+session.request.AccountHost+"'")
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0] != expected {
			return failure("DEPLOY_ACCOUNT_CONFLICT", "应用或迁移账号锁定状态偏离原用途，保留账号等待核实", false, false)
		}
	}
	return nil
}

// verifyScopedPrivileges 将角色、范围、授权转授和受保护迁移表分别核实。
func (session *BindingSession) verifyScopedPrivileges(ctx context.Context, user, grantee string, requireApplication bool) error {
	rows, err := session.sql.query(ctx, "SELECT CONCAT(TABLE_SCHEMA,':',PRIVILEGE_TYPE,':',IS_GRANTABLE) FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE="+grantee+" ORDER BY TABLE_SCHEMA,PRIVILEGE_TYPE")
	if err != nil {
		return err
	}
	for _, row := range rows {
		parts := strings.Split(row, ":")
		if user != session.request.MigrationUser || len(parts) != 3 || strings.ReplaceAll(parts[0], `\_`, "_") != session.request.Binding.DatabaseName || !slices.Contains(migrationPrivileges, parts[1]) || parts[2] != "NO" {
			return failure("DEPLOY_PRIVILEGES_CONFLICT", "用途账号出现库级或跨库权限漂移", false, false)
		}
	}
	rows, err = session.sql.query(ctx, "SELECT CONCAT(TABLE_SCHEMA,':',TABLE_NAME,':',PRIVILEGE_TYPE,':',IS_GRANTABLE) FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE="+grantee+" ORDER BY TABLE_SCHEMA,TABLE_NAME,PRIVILEGE_TYPE")
	if err != nil {
		return err
	}
	if requireApplication && user == session.request.ApplicationUser && len(rows) == 0 {
		return failure("DEPLOY_PRIVILEGES_UNKNOWN", "业务表权限未确认", false, true)
	}
	for _, row := range rows {
		parts := strings.Split(row, ":")
		if user != session.request.ApplicationUser || len(parts) != 4 || parts[0] != session.request.Binding.DatabaseName || !slices.Contains(session.applicationTablePrivileges(parts[1]), parts[2]) || parts[3] != "NO" {
			return failure("DEPLOY_PRIVILEGES_CONFLICT", "用途账号出现额外表权限", false, false)
		}
	}
	if requireApplication && user == session.request.ApplicationUser {
		return session.verifyApplicationGrantSet(ctx, rows)
	}
	return nil
}

// applicationTablePrivileges 尊重已保存的用途，迁移与绑定元数据无论用途都仅SELECT。
func (session *BindingSession) applicationTablePrivileges(table string) []string {
	if session.request.Binding.Purpose == "readOnly" || table == "schema_migrations" || table == "service_migration_digests" || table == "twine_binding_identity" {
		return []string{"SELECT"}
	}
	return applicationPrivileges
}

// verifyApplicationGrantSet 被动核实也检测缺失权限，不用非空授权或健康探针掩盖被撤销的业务用途能力。
func (session *BindingSession) verifyApplicationGrantSet(ctx context.Context, actual []string) error {
	tables, err := session.sql.query(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA="+sqlText(session.request.Binding.DatabaseName)+" AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME")
	if err != nil {
		return err
	}
	if len(tables) == 0 || len(tables) > 128 {
		return failure("DEPLOY_TABLES_UNSUPPORTED", "原业务表集合缺失或超出支持范围", false, false)
	}
	want := map[string]bool{}
	for _, table := range tables {
		if !businessDatabase.MatchString(table) {
			return failure("DEPLOY_TABLES_UNSUPPORTED", "原业务表标识不受支持", false, false)
		}
		for _, privilege := range session.applicationTablePrivileges(table) {
			want[session.request.Binding.DatabaseName+":"+table+":"+privilege+":NO"] = true
		}
	}
	if len(want) != len(actual) {
		return failure("DEPLOY_PRIVILEGES_CONFLICT", "应用所需表权限缺失或出现额外权限，保留原授权等待核实", false, false)
	}
	for _, row := range actual {
		if !want[row] {
			return failure("DEPLOY_PRIVILEGES_CONFLICT", "应用权限与原用途表集合不一致，保留原授权等待核实", false, false)
		}
	}
	return nil
}
