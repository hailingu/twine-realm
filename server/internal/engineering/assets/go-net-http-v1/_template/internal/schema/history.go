// 本文件核实实际迁移版本、dirty 与已应用内容，不自动修改历史。
package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
)

// State 将未迁移、脏状态和内容证据缺失分别表达，不把版本号当就绪。
type State struct {
	Version   *uint  `json:"version"`
	Dirty     bool   `json:"dirty"`
	Integrity string `json:"integrity"`
}

// Ready 只接受当前源码需要的干净版本及完整摘要证据。
func (state State) Ready(set Set) bool {
	return state.Version != nil && *state.Version == set.Latest && !state.Dirty && state.Integrity == "verified"
}

// Inspect 只读取当前业务库，不创建迁移历史或更正 dirty 标记。
func Inspect(ctx context.Context, db *sql.DB, set Set) (state State, err error) {
	state.Integrity = "missing"
	var version uint
	err = db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations LIMIT 1").Scan(&version, &state.Dirty)
	if errors.Is(err, sql.ErrNoRows) || missingTable(err) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("无法核实迁移历史")
	}
	state.Version = &version
	if state.Dirty {
		return state, nil
	}
	state.Integrity, err = inspectDigests(ctx, db, set, version)
	return state, err
}

// inspectDigests 保留历史与内容不匹配的事实，不用当前源码覆盖原摘要。
func inspectDigests(ctx context.Context, db *sql.DB, set Set, version uint) (integrity string, err error) {
	rows, err := db.QueryContext(ctx, "SELECT version,digest FROM service_migration_digests ORDER BY version")
	if missingTable(err) {
		return "missing", nil
	}
	if err != nil {
		return "unknown", fmt.Errorf("无法核实迁移内容摘要")
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	seen := map[uint]bool{}
	for rows.Next() {
		var number uint
		var digest string
		if err := rows.Scan(&number, &digest); err != nil {
			return "unknown", fmt.Errorf("迁移内容证据不可读取")
		}
		if number > version || set.Digests[number] != digest {
			return "mismatch", nil
		}
		seen[number] = true
	}
	if err := rows.Err(); err != nil {
		return "unknown", fmt.Errorf("迁移内容证据读取未完成")
	}
	for number := range set.Digests {
		if number <= version && !seen[number] {
			return "missing", nil
		}
	}
	if set.Digests[version] == "" {
		return "mismatch", nil
	}
	return "verified", nil
}

// missingTable 只将明确不存在的表判为尚未初始化，不吞掉连接或权限错误。
func missingTable(err error) bool {
	var failure *mysql.MySQLError
	return errors.As(err, &failure) && failure.Number == 1146
}

// Record 仅补入本次新应用版本的证据；不能覆盖已应用版本的内容。
func Record(ctx context.Context, db *sql.DB, set Set, previous *uint, current uint) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("无法保存本次迁移内容证据")
	}
	defer func() {
		if rollback := tx.Rollback(); !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	for version, digest := range set.Digests {
		if version > current || previous != nil && version <= *previous {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO service_migration_digests(version,digest) VALUES(?,?)", version, digest); err != nil {
			return fmt.Errorf("本次迁移内容证据未完成，须人工核实")
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("本次迁移内容证据结果未知，须人工核实")
	}
	return nil
}
