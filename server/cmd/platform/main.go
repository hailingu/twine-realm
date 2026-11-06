// 平台入口只装配本机配置、数据库、用例与 HTTP 服务。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/config"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
	"github.com/hailingu/twine-realm/server/internal/store"
)

// main 报告安全的启动诊断，并在所有资源已清理后退出。
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("平台停止", "reason", err.Error())
		os.Exit(1)
	}
}

// run 在数据库和迁移已就绪后才开放本机 HTTP。
func run(logger *slog.Logger) (err error) {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	inspector, err := gitworkspace.New(configuration.RepositoryRoots)
	if err != nil {
		return err
	}
	starting, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	database, err := store.Open(starting, configuration.DSN)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	definitions, err := catalog.New()
	if err != nil {
		return fmt.Errorf("装配离线组件目录失败")
	}
	running, err := configureRuntime(starting, configuration, database, definitions, logger)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, running.close()) }()
	building, err := configureBuild(starting, configuration, database, inspector, logger)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, building.close()) }()
	deploying, err := configureDeployment(configuration, database, running, building, logger)
	if err != nil {
		return err
	}
	cancel()
	service := project.NewService(database, database, database, inspector, definitions)
	handler, err := httpapi.New(service, configuration.Address, configuration.Origins, configuration.WebDirectory, logger, httpapi.Features{Runtime: running.service, Build: building.service, Deploy: deploying.service, State: projectstate.NewService(database)})
	if err != nil {
		return fmt.Errorf("装配 HTTP 契约失败")
	}
	tasks := platformTasks(running, building, deploying)
	return listenAndServe(ctx, configuration.Address, handler, tasks, logger)
}
