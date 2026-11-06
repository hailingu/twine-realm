// Package store 以 MySQL 保存项目聚合与同事务回执，仓库源码不在此存储。
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store 实现项目用例要求的显式原子写端口和读取端口。
type Store struct{ db *sql.DB }

// Open 校验本机连接、等待数据库并应用版本化迁移；调用者负责关闭。
func Open(ctx context.Context, dsn string) (*Store, error) {
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil || configuration.DBName == "" {
		return nil, fmt.Errorf("平台数据库配置无效")
	}
	host, _, err := net.SplitHostPort(configuration.Addr)
	ip := net.ParseIP(host)
	if err != nil || configuration.Net != "tcp" || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("平台数据库必须使用本机回环 TCP 地址")
	}
	configuration.ParseTime, configuration.MultiStatements = true, true
	configuration.Loc = time.UTC
	configuration.Timeout, configuration.ReadTimeout, configuration.WriteTimeout = 5*time.Second, 10*time.Second, 10*time.Second
	db, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("初始化平台数据库失败")
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("平台数据库未就绪"), db.Close())
	}
	if err := migrateSchema(db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := migrateRuntimeSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := migrateBuildSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := migrateDeploymentSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("关闭迁移连接失败")
	}
	configuration.MultiStatements = false
	db, err = sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("初始化平台数据库失败")
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)
	return &Store{db: db}, nil
}

// Close 释放平台独占的数据库连接池。
func (store *Store) Close() error { return store.db.Close() }

// migrateSchema 只执行随版本发布的迁移，不以业务代码生成 DDL。
func migrateSchema(db *sql.DB) error {
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("打开平台迁移失败: %w", err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("初始化平台迁移失败")
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return fmt.Errorf("创建平台迁移器失败")
	}
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("平台数据库迁移失败，请检查迁移版本")
	}
	return source.Close()
}
