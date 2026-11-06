// 本文件为构建模块持有独立迁移历史，同库升级不占用已发布核心序号。
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed build-migrations/*.sql
var buildMigrations embed.FS

// migrateBuildSchema 先在真实元数据库应用受控SQL，再允许受理构建。
func migrateBuildSchema(ctx context.Context, db *sql.DB) error {
	return migrateModule(ctx, db, buildMigrations, "build-migrations", "build_schema_migrations")
}
