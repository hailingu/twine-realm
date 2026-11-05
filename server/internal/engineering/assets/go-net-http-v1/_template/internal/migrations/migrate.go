// Package migrations 以独立迁移账号执行当前源码内的 SQL，保留 dirty 与摘要异常现场。
package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"example.invalid/service/internal/config"
	"example.invalid/service/internal/database"
	"example.invalid/service/internal/schema"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
)

// Apply 的 SQL 锁覆盖摘要核实、工具迁移及证据提交；DDL 不声明整体事务回滚。
func Apply(ctx context.Context, configuration config.Database) (err error) {
	set, err := schema.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(configuration, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	release, err := acquire(ctx, db, configuration.Name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	before, err := schema.Inspect(ctx, db, set)
	if err != nil {
		return err
	}
	if before.Dirty || before.Version != nil && before.Integrity != "verified" {
		return fmt.Errorf("迁移历史为 dirty 或摘要未核实，须人工处理")
	}
	runner, err := openRunner(ctx, db, configuration.Name)
	if err != nil {
		return err
	}
	defer func() { sourceErr, databaseErr := runner.Close(); err = errors.Join(err, sourceErr, databaseErr) }()
	if err := applyUntilStopped(ctx, runner); err != nil {
		return err
	}
	version, dirty, err := runner.Version()
	if err != nil || dirty || version != set.Latest {
		return fmt.Errorf("迁移尚未达到当前源码要求的干净版本")
	}
	return schema.Record(ctx, db, set, before.Version, version)
}

// openRunner 只让驱动拥有自己的连接，避免关闭摘要锁所在的连接池。
func openRunner(ctx context.Context, db *sql.DB, name string) (*migrate.Migrate, error) {
	source, err := schema.Source()
	if err != nil {
		return nil, fmt.Errorf("内嵌迁移不可读取")
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("迁移数据库不可达"), source.Close())
	}
	driver, err := migratemysql.WithConnection(ctx, connection, &migratemysql.Config{DatabaseName: name, StatementTimeout: 10 * time.Second})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("迁移驱动尚未就绪"), source.Close(), connection.Close())
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("迁移工具初始化失败"), source.Close(), driver.Close())
	}
	runner.LockTimeout = 10 * time.Second
	return runner, nil
}

// applyUntilStopped 核实在途 SQL 后退出，取消不清 dirty、不强制改版本或倒放 DDL。
func applyUntilStopped(ctx context.Context, runner *migrate.Migrate) error {
	if ctx.Err() != nil {
		return fmt.Errorf("迁移已取消，须查询实际历史后再决定接续")
	}
	done := make(chan error, 1)
	go func() { done <- runner.Up() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("迁移未完成，保留数据库与历史供核实")
		}
		return nil
	case <-ctx.Done():
		runner.GracefulStop <- true
		if err := <-done; err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("迁移已取消且在途 SQL 未完成，须核实数据库与历史")
		}
		return fmt.Errorf("迁移已取消，在途 SQL 已退出，须查询实际历史")
	}
}
