// 服务入口只装配配置、SQL、迁移和 HTTP，不在常驻应用启动时自动执行 DDL。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.invalid/service/internal/app"
	"example.invalid/service/internal/config"
	"example.invalid/service/internal/database"
	"example.invalid/service/internal/migrations"
	"example.invalid/service/internal/schema"
)

// main 只输出安全诊断，数据库与密码内容不进入错误上下文。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		slog.Error("服务命令未完成", "reason", err.Error())
		os.Exit(1)
	}
}

// run 固定命令集合；不开放 force、降级或任意 SQL / shell 入口。
func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) (err error) {
	if len(args) != 1 || args[0] != "serve" && args[0] != "migrate" && args[0] != "migration-status" {
		return fmt.Errorf("命令须为 serve、migrate 或 migration-status")
	}
	configuration, err := config.Load(getenv, args[0] == "migrate")
	if err != nil {
		return err
	}
	if args[0] == "migrate" {
		limited, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return migrations.Apply(limited, configuration.Database)
	}
	db, err := database.Open(configuration.Database, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if args[0] == "migration-status" {
		set, err := schema.Load()
		if err != nil {
			return err
		}
		limited, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		state, err := schema.Inspect(limited, db, set)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(state)
	}
	backend, err := database.NewStore(db)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", configuration.Address)
	if err != nil {
		return fmt.Errorf("HTTP 监听失败，请检查配置与端口")
	}
	return app.Serve(ctx, listener, app.New(configuration.Name, backend))
}
