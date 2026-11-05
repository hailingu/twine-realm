// 本文件只装配显式启用的本机运行能力，未配置时仍保留正常项目与历史查询。
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/config"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/store"
)

// runtimeServices 明确目录句柄和后台循环的所有权，不在装配阶段启动goroutine。
type runtimeServices struct {
	service *mysqlruntime.Service
	worker  *mysqlruntime.Worker
	driver  *mysqlruntime.Driver
}

// configureRuntime 在元数据库与私有根安装身份一致后才允许接受资源目标。
func configureRuntime(ctx context.Context, configuration config.Config, database *store.Store, definitions *catalog.Catalog, logger *slog.Logger) (value runtimeServices, err error) {
	if configuration.RuntimeAdapter != "" {
		forbidden, err := mysqlruntime.LocalForbidden(configuration.RepositoryRoots, configuration.WebDirectory)
		if err != nil {
			return value, err
		}
		driver, err := mysqlruntime.OpenDriver(configuration.RuntimeDirectory, forbidden)
		if err != nil {
			return value, err
		}
		value.driver = driver
		defer func() {
			if err != nil {
				err = errors.Join(err, driver.Close())
			}
		}()
		if err = database.BindRuntime(ctx, driver.InstallationID(), driver.Environment().DaemonID); err != nil {
			return value, err
		}
		value.worker, err = mysqlruntime.NewWorker(database, database, driver, mysqlruntime.DefaultPolicy(), logger)
		if err != nil {
			return value, err
		}
	}
	value.service = mysqlruntime.NewService(database, database, database, definitions, value.driver)
	return value, nil
}

// close 只能在工作器已退出后调用，不改变业务容器、卷或数据。
func (value runtimeServices) close() error {
	if value.driver != nil {
		return value.driver.Close()
	}
	return nil
}
