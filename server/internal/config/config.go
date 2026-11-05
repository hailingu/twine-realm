// Package config 仅从环境注入本机平台配置，秘密不进入前端或元数据库。
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
)

// Config 描述一期本地监听、仓库允许范围与平台自用数据库。
type Config struct {
	Address         string
	DSN             string
	RepositoryRoots []string
	Origins         []string
	WebDirectory    string
}

// Load 拒绝外部监听和泛化来源，不读取或建立用户账号。
func Load() (Config, error) {
	value := Config{Address: os.Getenv("TWINE_ADDRESS"), DSN: os.Getenv("TWINE_DATABASE_DSN"), WebDirectory: os.Getenv("TWINE_WEB_DIRECTORY")}
	if value.Address == "" {
		value.Address = "127.0.0.1:8080"
	}
	if value.WebDirectory == "" {
		value.WebDirectory = "web/dist"
	}
	if !loopback(value.Address) {
		return value, fmt.Errorf("监听地址必须为明确的回环 IP 和端口")
	}
	if value.DSN == "" {
		return value, fmt.Errorf("请通过环境配置平台数据库")
	}
	if err := json.Unmarshal([]byte(os.Getenv("TWINE_REPOSITORY_ROOTS")), &value.RepositoryRoots); err != nil || len(value.RepositoryRoots) == 0 {
		return value, fmt.Errorf("仓库允许目录必须为非空 JSON 数组")
	}
	value.Origins = []string{"http://" + value.Address}
	if origin := os.Getenv("TWINE_DEV_ORIGIN"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "http" || !loopback(parsed.Host) || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return value, fmt.Errorf("开发来源必须是明确的本机 HTTP 地址")
		}
		value.Origins = append(value.Origins, origin)
	}
	return value, nil
}

// loopback 同时校验 IP 与非零端口，阻止宽泛域名和系统自动暴露。
func loopback(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	parsed := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	return parsed != nil && parsed.IsLoopback() && err == nil && number > 0 && number <= 65535
}
