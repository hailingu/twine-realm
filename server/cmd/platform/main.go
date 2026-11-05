// 平台入口只装配本机配置、数据库、用例与 HTTP 服务。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/config"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/project"
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
	inspector, err := gitworkspace.New(configuration.RepositoryRoots)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	database, err := store.Open(ctx, configuration.DSN)
	cancel()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	definitions, err := catalog.New()
	if err != nil {
		return fmt.Errorf("装配离线组件目录失败")
	}
	service := project.NewService(database, database, database, inspector, definitions)
	handler, err := httpapi.New(service, configuration.Address, configuration.Origins, configuration.WebDirectory, logger)
	if err != nil {
		return fmt.Errorf("装配 HTTP 契约失败")
	}
	listener, err := net.Listen("tcp", configuration.Address)
	if err != nil {
		return fmt.Errorf("本机监听失败，请检查端口是否占用")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	logger.Info("平台已就绪", "address", configuration.Address)
	return serveUntilSignal(server, stopped)
}

// serveUntilSignal 等待退出信号并等待在途请求完成，进程重启不清空元数据。
func serveUntilSignal(server *http.Server, stopped <-chan error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("本机服务异常停止")
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(fmt.Errorf("本机服务未能正常退出"), server.Close())
		}
		if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("本机服务退出失败")
		}
		return nil
	}
}
