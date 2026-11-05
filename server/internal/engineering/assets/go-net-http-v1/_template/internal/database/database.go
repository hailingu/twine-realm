// Package database 为常驻应用与短期迁移分别构造有界 SQL 连接。
package database

import (
	"database/sql"
	"fmt"
	"net"
	"time"

	"example.invalid/service/internal/config"
	"github.com/go-sql-driver/mysql"
)

// Open 不将数据库不可达解释成进程死亡，实际就绪由独立探针核实。
func Open(configuration config.Database, migration bool) (*sql.DB, error) {
	password, err := config.ReadPassword(configuration.PasswordFile)
	if err != nil {
		return nil, err
	}
	settings := mysql.NewConfig()
	settings.User, settings.Passwd = configuration.User, password
	settings.Net, settings.Addr, settings.DBName = "tcp", net.JoinHostPort(configuration.Host, configuration.Port), configuration.Name
	settings.ParseTime, settings.Loc, settings.MultiStatements = true, time.UTC, migration
	settings.Timeout, settings.ReadTimeout, settings.WriteTimeout = 3*time.Second, 15*time.Second, 5*time.Second
	connection, err := mysql.NewConnector(settings)
	if err != nil {
		return nil, fmt.Errorf("数据库连接配置无效")
	}
	db := sql.OpenDB(connection)
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}
