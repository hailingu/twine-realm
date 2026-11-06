// 本文件只读核实固定业务迁移；不执行force、不清dirty、不覆盖历史摘要。
package mysqlruntime

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4/database"
)

// MigrationHistory 区分空库、已应用干净版本、dirty及缺失或冲突的内容证据。
type MigrationHistory struct {
	Version   uint64
	Integrity string
}

// History 与原迁移器使用相同会话锁；旧进程未退出时不能读半途状态并重跑DDL。
func (session *BindingSession) History(ctx context.Context, expected map[uint64]string) (value MigrationHistory, err error) {
	value.Integrity = "unknown"
	lock, err := database.GenerateAdvisoryLockId(session.request.Binding.DatabaseName + ":schema_migrations")
	if err != nil {
		return value, err
	}
	rows, err := session.sql.query(ctx, "SELECT GET_LOCK('"+lock+"',0)")
	if err != nil {
		return value, err
	}
	if len(rows) != 1 || rows[0] != "1" {
		return value, failure("DEPLOY_MIGRATION_IN_PROGRESS", "原迁移会话仍持有锁，不能重跑或判定干净", true, true)
	}
	value, err = session.readHistory(ctx, expected)
	if err != nil {
		return value, err
	}
	rows, err = session.sql.query(ctx, "SELECT RELEASE_LOCK('"+lock+"')")
	if err != nil {
		return value, err
	}
	if len(rows) != 1 || rows[0] != "1" {
		return value, failure("DEPLOY_MIGRATION_LOCK_UNKNOWN", "迁移核实锁未确认释放，禁止启动新任务", false, true)
	}
	return value, nil
}

// readHistory 先核实表存在再读取，SQL错误不会被吞掉当成尚未迁移。
func (session *BindingSession) readHistory(ctx context.Context, expected map[uint64]string) (MigrationHistory, error) {
	value := MigrationHistory{Integrity: "missing"}
	if err := session.verifyBusinessIdentity(ctx); err != nil {
		return value, err
	}
	databaseName := sqlText(session.request.Binding.DatabaseName)
	rows, err := session.sql.query(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA="+databaseName+" ORDER BY TABLE_NAME")
	if err != nil {
		return value, err
	}
	if len(rows) == 1 && rows[0] == "twine_binding_identity" {
		return value, nil
	}
	if !contains(rows, "schema_migrations") || !contains(rows, "service_migration_digests") {
		return value, failure("DEPLOY_MIGRATION_HISTORY_MISSING", "已有业务表但迁移历史不完整，禁止重复DDL", false, true)
	}
	value, err = session.readMigrationVersion(ctx)
	if err != nil || value.Integrity == "dirty" {
		return value, err
	}
	rows, err = session.sql.query(ctx, "SELECT CONCAT(version,':',digest) FROM "+sqlIdentifier(session.request.Binding.DatabaseName)+".`service_migration_digests` ORDER BY version")
	if err != nil {
		return value, err
	}
	value.Integrity, err = migrationIntegrity(rows, value.Version, expected)
	return value, err
}

// readMigrationVersion 严格解析单一版本和dirty事实，不将无记录或坏格式当作空库。
func (session *BindingSession) readMigrationVersion(ctx context.Context) (value MigrationHistory, err error) {
	value.Integrity = "missing"
	rows, err := session.sql.query(ctx, "SELECT CONCAT(version,':',dirty) FROM "+sqlIdentifier(session.request.Binding.DatabaseName)+".`schema_migrations`")
	if err != nil {
		return value, err
	}
	if len(rows) != 1 {
		return value, failure("DEPLOY_MIGRATION_HISTORY_MISSING", "迁移版本记录缺失或不唯一，须核实原迁移", false, true)
	}
	parts := strings.Split(rows[0], ":")
	if len(parts) != 2 {
		return value, fmt.Errorf("迁移版本事实无效")
	}
	value.Version, err = strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return value, fmt.Errorf("迁移版本事实无效")
	}
	if parts[1] == "1" {
		value.Integrity = "dirty"
		return value, nil
	}
	if parts[1] != "0" {
		return value, fmt.Errorf("迁移dirty事实无效")
	}
	return value, nil
}

// migrationIntegrity 单独比对已应用内容，重复、未知或较新版本均不能被伪报为匹配。
func migrationIntegrity(rows []string, version uint64, expected map[uint64]string) (string, error) {
	seen := map[uint64]bool{}
	for _, row := range rows {
		parts := strings.Split(row, ":")
		if len(parts) != 2 {
			return "unknown", fmt.Errorf("迁移摘要事实无效")
		}
		number, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil || number > version || expected[number] != parts[1] || seen[number] {
			return "mismatch", nil
		}
		seen[number] = true
	}
	latest := uint64(0)
	for number := range expected {
		latest = max(latest, number)
		if number <= version && !seen[number] {
			return "missing", nil
		}
	}
	if version != latest || expected[version] == "" {
		return "mismatch", nil
	}
	return "verified", nil
}
