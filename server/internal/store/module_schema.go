// 本文件只为真实独立运行与构建模块共享迁移连接所有权，不生成业务DDL。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrateModule 在独立历史执行版本化迁移；失败保留dirty及原数据，不自动修复。
func migrateModule(ctx context.Context, db *sql.DB, files fs.FS, directory, table string) (err error) {
	runner, err := moduleRunner(ctx, db, files, directory, table)
	if err != nil {
		return err
	}
	defer func() { sourceErr, databaseErr := runner.Close(); err = errors.Join(err, sourceErr, databaseErr) }()
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("模块元数据迁移失败，保留原数据并停止写入")
	}
	return nil
}

// moduleRunner 只转移独立连接的所有权，不能关闭应用共享连接池。
func moduleRunner(ctx context.Context, db *sql.DB, files fs.FS, directory, table string) (*migrate.Migrate, error) {
	source, err := iofs.New(files, directory)
	if err != nil {
		return nil, fmt.Errorf("打开模块迁移失败")
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("模块迁移连接不可用"), source.Close())
	}
	driver, err := migratemysql.WithConnection(ctx, connection, &migratemysql.Config{MigrationsTable: table, StatementTimeout: 10 * time.Second})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("初始化模块迁移失败"), source.Close(), connection.Close())
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("创建模块迁移器失败"), source.Close(), driver.Close())
	}
	runner.LockTimeout = 10 * time.Second
	return runner, nil
}
