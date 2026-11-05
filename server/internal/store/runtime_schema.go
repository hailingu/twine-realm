// 本文件为独立运行模块使用单独迁移历史，不与未合并核心切片争用迁移序号。
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed runtime-migrations/*.sql
var runtimeMigrations embed.FS

// migrateRuntimeSchema 仍在同一元数据库执行版本化SQL，不以运行代码临时建表。
func migrateRuntimeSchema(ctx context.Context, db *sql.DB) (err error) {
	runner, err := runtimeRunner(ctx, db)
	if err != nil {
		return err
	}
	defer func() { sourceErr, databaseErr := runner.Close(); err = errors.Join(err, sourceErr, databaseErr) }()
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("运行元数据迁移失败，保留原数据并停止写入")
	}
	return nil
}

// runtimeRunner 仅转移独立连接的所有权，不让迁移器关闭共享连接池。
func runtimeRunner(ctx context.Context, db *sql.DB) (*migrate.Migrate, error) {
	source, err := iofs.New(runtimeMigrations, "runtime-migrations")
	if err != nil {
		return nil, fmt.Errorf("打开运行元数据迁移失败")
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("运行迁移连接不可用"), source.Close())
	}
	driver, err := migratemysql.WithConnection(ctx, connection, &migratemysql.Config{MigrationsTable: "runtime_schema_migrations", StatementTimeout: 10 * time.Second})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("初始化运行元数据迁移失败"), source.Close(), connection.Close())
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("创建运行元数据迁移器失败"), source.Close(), driver.Close())
	}
	runner.LockTimeout = 10 * time.Second
	return runner, nil
}
