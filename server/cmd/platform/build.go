// 本文件仅装配明确启用的隔离构建能力，默认保持禁用且可读取已有历史。
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hailingu/twine-realm/server/internal/config"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/store"
)

// buildServices 明确目录与工作器的生命周期，不在装配阶段启动副作用。
type buildServices struct {
	service *servicebuild.Service
	worker  *servicebuild.Worker
	driver  *servicebuild.Driver
}

// configureBuild 将原私有根、工具环境和元数据库绑定；不静默初始化新根接管历史。
func configureBuild(ctx context.Context, configuration config.Config, database *store.Store, inspector *gitworkspace.Inspector, logger *slog.Logger) (value buildServices, err error) {
	if configuration.BuildAdapter != "" {
		driver, err := openBuildDriver(ctx, configuration, database)
		if err != nil {
			return value, err
		}
		value.driver = driver
		defer func() {
			if err != nil {
				err = errors.Join(err, driver.Close())
			}
		}()
		value.worker, err = servicebuild.NewWorker(database, database, inspector, driver, logger)
		if err != nil {
			return value, err
		}
	}
	value.service, err = servicebuild.NewService(database, database, database, inspector, value.driver)
	return value, err
}

// close 在全部工作器退出后释放私有根，不删除任何材料或候选镜像。
func (value buildServices) close() error {
	if value.driver != nil {
		return value.driver.Close()
	}
	return nil
}

// openBuildDriver 先核实私有根与同库绑定，失败时关闭新取得的句柄。
func openBuildDriver(ctx context.Context, configuration config.Config, database *store.Store) (driver *servicebuild.Driver, err error) {
	forbidden, err := privatefiles.LocalForbidden(configuration.RepositoryRoots, configuration.WebDirectory)
	if err != nil {
		return nil, err
	}
	if configuration.RuntimeDirectory != "" {
		forbidden = append(forbidden, configuration.RuntimeDirectory)
	}
	driver, err = servicebuild.OpenDriver(configuration.BuildDirectory, forbidden)
	if err != nil {
		return nil, err
	}
	opened := driver
	defer func() {
		if err != nil {
			err = errors.Join(err, opened.Close())
		}
	}()
	digest, err := servicebuild.Fingerprint(driver.Environment())
	if err != nil {
		return nil, err
	}
	err = database.BindBuild(ctx, driver.InstallationID(), digest)
	return driver, err
}
