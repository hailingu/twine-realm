// 本文件仅装配显式业务部署，默认不创建账号、业务数据库或容器。
package main

import (
	"log/slog"

	"github.com/hailingu/twine-realm/server/internal/config"
	"github.com/hailingu/twine-realm/server/internal/servicecompose"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"github.com/hailingu/twine-realm/server/internal/store"
)

// deploymentServices 借用原运行与构建句柄，其关闭仍由原所有者负责。
type deploymentServices struct {
	service *servicedeploy.Service
	worker  *servicedeploy.Worker
}

// configureDeployment 在启动工作器前注册Compose扩展，不在装配时执行部署。
func configureDeployment(configuration config.Config, database *store.Store, running runtimeServices, building buildServices, logger *slog.Logger) (value deploymentServices, err error) {
	var adapter servicedeploy.Adapter
	if configuration.DeployAdapter != "" {
		driver, err := servicecompose.NewDriver(database, running.driver, building.driver)
		if err != nil {
			return value, err
		}
		adapter = driver
		value.worker, err = servicedeploy.NewWorker(database, driver, logger)
		if err != nil {
			return value, err
		}
	}
	value.service = servicedeploy.NewService(database, database, adapter)
	return value, nil
}

// platformTasks 收集仅已明确启用的后台任务，保持统一关闭与等待。
func platformTasks(running runtimeServices, building buildServices, deploying deploymentServices) *backgroundTasks {
	tasks := &backgroundTasks{}
	if running.worker != nil {
		tasks.values = append(tasks.values, running.worker)
	}
	if building.worker != nil {
		tasks.values = append(tasks.values, building.worker)
	}
	if deploying.worker != nil {
		tasks.values = append(tasks.values, deploying.worker)
	}
	return tasks
}
