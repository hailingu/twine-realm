// 本机运行初始化只核实Docker并创建全新私有根，不创建数据库容器或账号。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
)

// main 初始化失败保留目录供核实，输出只含非敏感引用。
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 在支持条件明确后才创建新根，绝不覆盖或自动接管已有材料。
func run(ctx context.Context, args []string, output io.Writer) (err error) {
	directory, err := arguments(args)
	if err != nil {
		return err
	}
	roots, err := repositoryRoots(os.Getenv("TWINE_REPOSITORY_ROOTS"))
	if err != nil {
		return err
	}
	forbidden, err := mysqlruntime.LocalForbidden(roots, os.Getenv("TWINE_WEB_DIRECTORY"))
	if err != nil {
		return err
	}
	if build := os.Getenv("TWINE_BUILD_DIRECTORY"); build != "" {
		forbidden = append(forbidden, build)
	}
	environment, err := mysqlruntime.DiscoverEnvironment(ctx)
	if err != nil {
		return err
	}
	if err := mysqlruntime.InitializeDirectory(directory, forbidden, environment); err != nil {
		return err
	}
	driver, err := mysqlruntime.OpenDriver(directory, forbidden)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, driver.Close()) }()
	return writeReport(output, driver.InstallationID(), driver.Environment())
}

// arguments 限定唯一显式新目录，不接受额外命令或Docker参数。
func arguments(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--directory" || !filepath.IsAbs(args[1]) {
		return "", fmt.Errorf("用法：runtime-init --directory /绝对路径/全新私有运行目录")
	}
	return args[1], nil
}

// repositoryRoots 初始化前显式核实仓库范围，避免把未来秘密放进源码。
func repositoryRoots(raw string) ([]string, error) {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil || len(values) == 0 {
		return nil, fmt.Errorf("请先配置非空TWINE_REPOSITORY_ROOTS数组")
	}
	for _, value := range values {
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("仓库允许根必须是绝对路径")
		}
	}
	return values, nil
}

// writeReport 写入失败保持非零，结果未知时不得改目录重复初始化。
func writeReport(output io.Writer, id string, environment mysqlruntime.Environment) error {
	return json.NewEncoder(output).Encode(struct {
		SchemaVersion  int    `json:"schemaVersion"`
		InstallationID string `json:"installationId"`
		Adapter        string `json:"adapter"`
		Context        string `json:"context"`
		DaemonID       string `json:"daemonId"`
	}{1, id, "file-secrets-v1", environment.Context, environment.DaemonID})
}
