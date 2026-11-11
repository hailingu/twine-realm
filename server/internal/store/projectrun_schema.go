// 本文件独立版本化项目生命周期账本，不修改组件历史。
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed projectrun-migrations/*.sql
var projectRunMigrations embed.FS

// migrateProjectRunSchema 在开放父操作前完成受控升级。
func migrateProjectRunSchema(ctx context.Context, db *sql.DB) error {
	return migrateModule(ctx, db, projectRunMigrations, "projectrun-migrations", "projectrun_schema_migrations")
}
