// 本文件保留运行模块独立的迁移历史与公开内部入口。
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed runtime-migrations/*.sql
var runtimeMigrations embed.FS

// migrateRuntimeSchema 在同库应用版本化运行SQL，不临时生成DDL。
func migrateRuntimeSchema(ctx context.Context, db *sql.DB) error {
	return migrateModule(ctx, db, runtimeMigrations, "runtime-migrations", "runtime_schema_migrations")
}
