//go:build integration

// 本文件让测试采样走生产读取入口，不能凭回执或时间猜测观察令牌。
package httpapi_test

import (
	"context"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"github.com/hailingu/twine-realm/server/internal/store"
)

// captureRuntimeTarget 从真实分页采集入口取得原目标及父观察令牌。
func captureRuntimeTarget(t *testing.T, s *store.Store, target mysqlruntime.Target) mysqlruntime.Target {
	t.Helper()
	after := ""
	for {
		values, err := s.RuntimeInstances(context.Background(), after)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if value.Target.ID == target.ID {
				return value.Target
			}
			after = value.Target.ProjectID
		}
		if len(values) < 16 {
			t.Fatal("原采样目标不在当前实例中")
		}
	}
}

// captureDeploymentTarget 从真实部署采样入口取得原目标及父观察令牌。
func captureDeploymentTarget(t *testing.T, s *store.Store, target servicedeploy.Target) servicedeploy.Target {
	t.Helper()
	values, err := s.DeploymentTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.ID == target.ID {
			return value
		}
	}
	t.Fatal("原采样目标不在当前部署中")
	return servicedeploy.Target{}
}
