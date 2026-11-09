// 本文件有界等待原数据库认证与原应用只读就绪，不执行修复。
package servicecompose

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"time"
)

// waitExistingDatabase 保持同一UUID，启动后仅允许等待数据库认证。
func (driver *Driver) waitExistingDatabase(ctx context.Context, state *existingState) (*mysqlruntime.Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(state.frozen.Deployment.Plan.Policy.ReadinessSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		fact, err := driver.runtime.Inspect(ctx, state.frozen.Runtime.Resource)
		if err == nil && fact != nil && fact.Ready {
			if fact.ContainerID != state.frozen.DatabaseContainerID || fact.ServerUUID != state.frozen.ServerUUID {
				return nil, existingConflict()
			}
			return fact, nil
		}
		select {
		case <-ctx.Done():
			return fact, existingConflict()
		case <-ticker.C:
		}
	}
}

// waitExistingApplication 原容器退出即失败，不用重新启动掩盖故障。
func (driver *Driver) waitExistingApplication(ctx context.Context, state *existingState, session *mysqlruntime.BindingSession) (*servicedeploy.Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(state.frozen.Deployment.Plan.Policy.ReadinessSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		fact, err := driver.inspectFacts(ctx, state.frozen.Deployment.Plan, session)
		if err != nil {
			return fact, err
		}
		if fact == nil || fact.ContainerID != state.frozen.ApplicationContainerID || !fact.Running {
			return fact, existingConflict()
		}
		if fact.Ready {
			return fact, nil
		}
		select {
		case <-ctx.Done():
			return fact, existingConflict()
		case <-ticker.C:
		}
	}
}
