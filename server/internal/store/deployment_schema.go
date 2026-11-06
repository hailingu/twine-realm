// 本文件为业务部署保留独立版本历史，不改写已发布核心、MySQL或构建迁移。
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed deployment-migrations/*.sql
var deploymentMigrations embed.FS

// migrateDeploymentSchema 在开放部署接口前应用同库受控迁移。
func migrateDeploymentSchema(ctx context.Context, db *sql.DB) error {
	return migrateModule(ctx, db, deploymentMigrations, "deployment-migrations", "deployment_schema_migrations")
}
