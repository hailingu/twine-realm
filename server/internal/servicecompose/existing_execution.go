// 本文件按父账本逐命令执行原资源启停，未知动作不能自动重放或反向执行。
package servicecompose

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"reflect"
	"time"
)

// existingRun 只持有本次父操作及回执回调，不保存context或组件伪租约。
type existingRun struct {
	driver *Driver
	lease  projectrun.Lease
	report func(context.Context, projectrun.Step) error
	facts  projectrun.Facts
}

// StopExisting 先停止原应用和迁移进程，最后停止原数据库。
func (driver *Driver) StopExisting(ctx context.Context, lease projectrun.Lease, report func(context.Context, projectrun.Step) error) (projectrun.Facts, error) {
	work := &existingRun{driver: driver, lease: lease, report: report}
	err := driver.runtime.WithExistingProject(ctx, lease.Target.ProjectID, func() error { return work.stop(ctx) })
	return work.facts, err
}

// stop 在停止业务并核实之后才允许停止数据库。
func (work *existingRun) stop(ctx context.Context) error {
	state, err := work.driver.matchExisting(ctx, work.lease)
	if err != nil {
		return err
	}
	if err := work.stopBusiness(ctx, state); err != nil {
		return err
	}
	state, err = work.driver.matchExisting(ctx, work.lease)
	if err != nil {
		return err
	}
	if state.application.State.Running || state.migration.State.Running {
		return existingConflict()
	}
	work.facts.ApplicationStopped = true
	work.facts.ApplicationObservedAt = mysqlruntime.Timestamp(time.Now())
	err = existingAction(ctx, work.lease, "stop_database", state.frozen.DatabaseContainerID, state.runtime.Running, work.report, func() error { return work.driver.changeDatabase(ctx, state, false) })
	if err != nil {
		return err
	}
	return work.confirmStopped(ctx)
}

// stopBusiness 分别登记应用与迁移停止，不将两个命令合为一个未知边界。
func (work *existingRun) stopBusiness(ctx context.Context, state *existingState) error {
	for _, item := range []struct {
		name, id string
		running  bool
	}{{"stop_application", state.application.ID, state.application.State.Running}, {"stop_migration", state.migration.ID, state.migration.State.Running}} {
		err := existingAction(ctx, work.lease, item.name, item.id, item.running, work.report, func() error {
			_, err := work.driver.run(ctx, state.frozen.Deployment.Plan, "stop", item.id)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// confirmStopped 重新核实全部原对象后才生成停止事实。
func (work *existingRun) confirmStopped(ctx context.Context) error {
	state, err := work.driver.matchExisting(ctx, work.lease)
	if err != nil {
		return err
	}
	if state.runtime.Running || state.application.State.Running || state.migration.State.Running {
		return existingConflict()
	}
	work.facts.DatabaseStopped = true
	work.facts.DatabaseObservedAt = state.runtime.ObservedAt
	work.facts.Runtime = state.runtime
	return nil
}

// ResumeExisting 只恢复原数据库和原应用，历史迁移任务从不再次启动。
func (driver *Driver) ResumeExisting(ctx context.Context, lease projectrun.Lease, report func(context.Context, projectrun.Step) error) (projectrun.Facts, error) {
	work := &existingRun{driver: driver, lease: lease, report: report}
	err := driver.runtime.WithExistingProject(ctx, lease.Target.ProjectID, func() error { return work.resume(ctx) })
	return work.facts, err
}

// resume 先启动原数据库并认证，再进入原绑定只读核实。
func (work *existingRun) resume(ctx context.Context) error {
	state, err := work.driver.matchExisting(ctx, work.lease)
	if err != nil {
		return err
	}
	if state.migration.State.Running {
		return existingConflict()
	}
	err = existingAction(ctx, work.lease, "start_database", state.frozen.DatabaseContainerID, !state.runtime.Running, work.report, func() error { return work.driver.changeDatabase(ctx, state, true) })
	if err != nil {
		return err
	}
	work.facts.Runtime, err = work.driver.waitExistingDatabase(ctx, state)
	if err != nil {
		return err
	}
	return work.driver.runtime.WithExistingBinding(ctx, state.request, func(session *mysqlruntime.BindingSession) error { return work.resumeBinding(ctx, state, session) })
}

// resumeBinding 验证权限与原迁移历史，成功证明落库后才能启动业务。
func (work *existingRun) resumeBinding(ctx context.Context, state *existingState, session *mysqlruntime.BindingSession) error {
	if err := session.Verify(ctx); err != nil {
		return err
	}
	expected, err := migrationDigests(state.frozen.Deployment.Plan.Candidate)
	if err != nil {
		return err
	}
	history, err := session.History(ctx, expected)
	if err != nil {
		return err
	}
	if history.Integrity != "verified" || state.migration.State.ExitCode != 0 {
		return existingConflict()
	}
	if err := existingAction(ctx, work.lease, "verify_database", state.frozen.DatabaseContainerID, false, work.report, func() error { return nil }); err != nil {
		return err
	}
	return work.resumeApplication(ctx, session)
}

// resumeApplication 完成原容器启动、HTTP核实与最终冻结身份复核。
func (work *existingRun) resumeApplication(ctx context.Context, session *mysqlruntime.BindingSession) error {
	state, err := work.driver.matchExisting(ctx, work.lease)
	if err != nil {
		return err
	}
	err = existingAction(ctx, work.lease, "start_application", state.application.ID, !state.application.State.Running, work.report, func() error {
		_, err := work.driver.run(ctx, state.frozen.Deployment.Plan, "start", state.application.ID)
		return err
	})
	if err != nil {
		return err
	}
	work.facts.Deployment, err = work.driver.waitExistingApplication(ctx, state, session)
	if err == nil {
		_, err = work.driver.matchExisting(ctx, work.lease)
	}
	work.facts.ApplicationObservedAt = mysqlruntime.Timestamp(time.Now())
	work.facts.DatabaseObservedAt = work.facts.Runtime.ObservedAt
	return err
}

// matchExisting 每个阶段重新核实冻结身份，绝不按同名新对象继续。
func (driver *Driver) matchExisting(ctx context.Context, lease projectrun.Lease) (*existingState, error) {
	state, err := driver.existing(ctx, lease.Target.Frozen.Runtime, lease.Target.Frozen.Deployment)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(state.frozen, lease.Target.Frozen) {
		return nil, existingConflict()
	}
	return state, nil
}

// changeDatabase 仅调用已有物理对象的受限单命令入口。
func (driver *Driver) changeDatabase(ctx context.Context, state *existingState, running bool) error {
	f := state.frozen
	return driver.runtime.ChangeExisting(ctx, f.Runtime.Resource, mysqlruntime.ExistingIdentity{ContainerID: f.DatabaseContainerID, NetworkID: f.DatabaseNetworkID, VolumeCreatedAt: f.DatabaseVolumeCreatedAt}, running)
}

// existingAction 不将旧执行中或未知动作解释成可重复命令。
func existingAction(ctx context.Context, lease projectrun.Lease, name, id string, needed bool, report func(context.Context, projectrun.Step) error, action func() error) error {
	for _, step := range lease.Operation.Steps {
		if step.State == "executing" || step.State == "unknown" {
			return existingConflict()
		}
	}
	for _, step := range lease.Operation.Steps {
		if step.Name == name && step.State == "succeeded" {
			if needed {
				return existingConflict()
			}
			return nil
		}
	}
	step := projectrun.Step{Name: name, State: "executing", ExternalID: id, UpdatedAt: mysqlruntime.Timestamp(time.Now())}
	if err := report(ctx, step); err != nil {
		return err
	}
	if needed {
		if err := action(); err != nil {
			step.State = "unknown"
			step.Code = "PROJECT_EXTERNAL_UNKNOWN"
			return errors.Join(err, report(ctx, step))
		}
	}
	step.State = "succeeded"
	step.UpdatedAt = mysqlruntime.Timestamp(time.Now())
	return report(ctx, step)
}
