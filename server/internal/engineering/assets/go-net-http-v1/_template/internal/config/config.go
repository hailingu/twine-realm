// Package config 只解析非敏感配置和按用途注入的密码文件路径。
package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
var databaseUser = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// Database 只保存连接位置、用途账号和文件引用，不包含密码内容。
type Database struct {
	Host         string
	Port         string
	Name         string
	User         string
	PasswordFile string
}

// Config 是服务启动配置；迁移模式只读取单独的迁移身份。
type Config struct {
	Address  string
	Name     string
	Database Database
}

// Load 允许显式容器监听，缺省仅本机；不支持从环境变量直接读取密码。
func Load(getenv func(string) string, migration bool) (Config, error) {
	value := Config{Address: getenv("HTTP_ADDRESS"), Name: getenv("SERVICE_NAME")}
	if value.Address == "" {
		value.Address = "127.0.0.1:8080"
	}
	if value.Name == "" {
		value.Name = "最小服务"
	}
	if !validAddress(value.Address) || len(value.Name) > 240 || strings.IndexFunc(value.Name, unicode.IsControl) >= 0 {
		return value, fmt.Errorf("服务名称或 HTTP 监听地址无效")
	}
	value.Database = Database{Host: getenv("DB_HOST"), Port: getenv("DB_PORT"), Name: getenv("DB_NAME"), User: getenv("DB_USER"), PasswordFile: getenv("DB_PASSWORD_FILE")}
	if migration {
		value.Database.User = getenv("MIGRATION_USER")
		value.Database.PasswordFile = getenv("MIGRATION_PASSWORD_FILE")
	}
	if value.Database.Port == "" {
		value.Database.Port = "3306"
	}
	if err := validDatabase(value.Database); err != nil {
		return value, err
	}
	return value, nil
}

// validAddress 限定为明确 IP 与非零端口，避免意外的模糊监听。
func validAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

// validDatabase 拒绝 DSN 片段和未声明身份，连接参数由驱动结构化构造。
func validDatabase(value Database) error {
	port, err := strconv.Atoi(value.Port)
	if err != nil || port < 1 || port > 65535 || value.Host == "" || len(value.Host) > 253 || strings.ContainsAny(value.Host, "/?@#\x00\r\n\t ") {
		return fmt.Errorf("数据库位置无效")
	}
	if !databaseName.MatchString(value.Name) || !databaseUser.MatchString(value.User) || value.PasswordFile == "" {
		return fmt.Errorf("数据库名称、用途账号或密码文件引用缺失或无效")
	}
	return nil
}
