// 本文件独立版本化短期项目通知，不修改或裁剪永久部署检查点。
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed state-migrations/*.sql
var stateMigrations embed.FS

// migrateStateSchema 在开放一致读取与通知前完成受控升级。
func migrateStateSchema(ctx context.Context, db *sql.DB) error {
	return migrateModule(ctx, db, stateMigrations, "state-migrations", "state_schema_migrations")
}
