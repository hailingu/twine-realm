// 本文件冻结和核实原项目物理资源，绝不补建缺失材料。
package servicecompose

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// existingState 保存同一次项目锁内的严格资源事实。
type existingState struct {
	frozen                 projectrun.Frozen
	runtime                *mysqlruntime.Observation
	application, migration *containerState
	request                mysqlruntime.BindingRequest
}

// Freeze 仅接受原应用和数据库均经认证就绪的实际资源。
func (driver *Driver) Freeze(ctx context.Context, runtime mysqlruntime.Target, deployment servicedeploy.Target) (frozen projectrun.Frozen, facts projectrun.Facts, err error) {
	err = driver.runtime.WithExistingProjectRead(ctx, runtime.ProjectID, func() error {
		state, err := driver.existing(ctx, runtime, deployment)
		if err != nil {
			return err
		}
		if !state.runtime.Running || !state.application.State.Running {
			return existingConflict()
		}
		deploymentFact, err := driver.verifyExistingReady(ctx, state)
		if err != nil {
			return err
		}
		runtimeFact, err := driver.runtime.Inspect(ctx, runtime.Resource)
		if err != nil {
			return err
		}
		if runtimeFact == nil || !runtimeFact.Ready || runtimeFact.ContainerID != state.frozen.DatabaseContainerID || runtimeFact.ServerUUID != state.frozen.ServerUUID {
			return existingConflict()
		}
		if deploymentFact.ContainerID != state.frozen.ApplicationContainerID || deploymentFact.DatabaseUUID != state.frozen.ServerUUID {
			return existingConflict()
		}
		facts = projectrun.Facts{Runtime: runtimeFact, Deployment: deploymentFact, DatabaseObservedAt: runtimeFact.ObservedAt, ApplicationObservedAt: deploymentFact.ObservedAt}
		frozen = state.frozen
		return nil
	})
	return frozen, facts, err
}

// existing 不从当前画布或新候选生成原恢复计划。
func (driver *Driver) existing(ctx context.Context, runtime mysqlruntime.Target, deployment servicedeploy.Target) (*existingState, error) {
	if runtime.ProjectID != deployment.ProjectID || deployment.Plan.ProjectID != runtime.ProjectID {
		return nil, existingConflict()
	}
	if err := driver.build.VerifyCandidate(ctx, deployment.Plan.Candidate); err != nil {
		return nil, err
	}
	request, err := driver.bindingRequest(ctx, deployment.Plan)
	if err != nil {
		return nil, err
	}
	originalDigest, err := mysqlruntime.Fingerprint(runtime.Resource)
	if err != nil || originalDigest != deployment.Plan.Database.ResourceDigest {
		return nil, existingConflict()
	}
	identity, fact, err := driver.runtime.InspectExisting(ctx, runtime.Resource)
	if err != nil {
		return nil, err
	}
	files, err := driver.runtime.BindingPaths(request)
	if err != nil {
		return nil, err
	}
	material, err := composeMaterial(deployment.Plan, files)
	if err != nil {
		return nil, err
	}
	if err := driver.runtime.VerifyExistingBindingMaterials(ctx, request, material); err != nil {
		return nil, err
	}
	application, err := driver.inspectContainer(ctx, deployment.Plan, "application", files)
	if err != nil {
		return nil, err
	}
	migration, err := driver.inspectContainer(ctx, deployment.Plan, "migration", files)
	if err != nil {
		return nil, err
	}
	ingress, err := driver.inspectIngress(ctx, deployment.Plan)
	if err != nil {
		return nil, err
	}
	if application == nil || migration == nil || ingress == nil || identity.VolumeCreatedAt == "" {
		return nil, existingConflict()
	}
	frozen := projectrun.Frozen{Runtime: runtime, Deployment: deployment, DatabaseContainerID: identity.ContainerID, DatabaseNetworkID: identity.NetworkID, DatabaseVolumeCreatedAt: identity.VolumeCreatedAt, ApplicationContainerID: application.ID, MigrationContainerID: migration.ID, IngressNetworkID: ingress.ID, ServerUUID: deployment.Plan.Database.ServerUUID}
	return &existingState{frozen, fact, application, migration, request}, nil
}

// existingConflict 明确拒绝缺失、漂移和无法确认的原资源。
func existingConflict() error {
	return servicedeploy.Failure("PROJECT_RESOURCE_CONFLICT", "原项目资源缺失、改变或尚未核实，禁止替代恢复", false)
}

// verifyExistingReady 在原锁内认证数据库并核实用途权限、历史及HTTP。
func (driver *Driver) verifyExistingReady(ctx context.Context, state *existingState) (fact *servicedeploy.Observation, err error) {
	err = driver.runtime.WithExistingBinding(ctx, state.request, func(session *mysqlruntime.BindingSession) error {
		if err := session.Verify(ctx); err != nil {
			return err
		}
		if state.migration.State.Running || state.migration.State.ExitCode != 0 {
			return existingConflict()
		}
		var err error
		fact, err = driver.inspectFacts(ctx, state.frozen.Deployment.Plan, session)
		if err != nil {
			return err
		}
		if fact == nil || !fact.Ready {
			return existingConflict()
		}
		return nil
	})
	return fact, err
}
