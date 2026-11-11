//go:build integration

// 本文件只破坏测试自有项目的迁移标记，验证父恢复不清除dirty或重新执行迁移。
package httpapi_test

import (
	"context"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"testing"
	"time"
)

// TestRealProjectRunPreservesDirty 在停止受理后发生外部dirty，恢复必须保留现场且不启动应用。
func TestRealProjectRunPreservesDirty(t *testing.T) {
	f := openDeploymentFixture(t)
	_, accepted := f.accept(t, "")
	t.Cleanup(func() { cleanupDeploymentContainers(t, accepted.Target.Plan) })
	deployed, err := f.execute(t, accepted)
	if err != nil || deployed.Observation == nil || !deployed.Observation.Ready {
		t.Fatal("初始真实部署失败", err)
	}
	assertTodoCRUD(t, deployed.Observation.EntryURL)
	f.refreshDatabase(t)
	database := f.runtime.base.database
	service := projectrun.NewService(database, database, f.adapter)
	ctx := context.Background()
	stopped, err := service.Accept(ctx, f.project.ID, projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "0", Desired: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	plan := accepted.Target.Plan
	migrationID := deploymentContainerID(t, plan, plan.Resources.Migration)
	deploymentSQL(t, plan, "UPDATE `"+plan.Binding.DatabaseName+"`.schema_migrations SET dirty=1; SELECT dirty FROM `"+plan.Binding.DatabaseName+"`.schema_migrations", "1")
	executePhysicalProjectRun(t, f, stopped)
	resumed, err := service.Accept(ctx, f.project.ID, projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "1", Desired: "running"})
	if err != nil {
		t.Fatal(err)
	}
	executeDirtyProjectResume(t, f, resumed)
	deploymentSQL(t, plan, "SELECT dirty FROM `"+plan.Binding.DatabaseName+"`.schema_migrations", "1")
	deploymentSQL(t, plan, "SELECT COUNT(*) FROM `"+plan.Binding.DatabaseName+"`.todos", "1")
	if deploymentContainerID(t, plan, plan.Resources.Migration) != migrationID {
		t.Fatal("迁移容器被替换")
	}
	snapshot, err := database.ProjectRunSnapshot(ctx, f.project.ID)
	if err != nil || snapshot.Operation.State != "failed" {
		t.Fatal("失败未持久", err)
	}
	for _, step := range snapshot.Operation.Steps {
		if step.Name == "start_application" {
			t.Fatal("dirty验证失败后仍启动应用")
		}
	}
}

// executeDirtyProjectResume 核实dirty拒绝启动应用并持久失败及已有事实。
func executeDirtyProjectResume(t *testing.T, f *deploymentFixture, resumed projectrun.Receipt) {
	t.Helper()
	ctx := context.Background()
	database := f.runtime.base.database
	lease, err := database.ClaimProjectRun(ctx, time.Now(), time.Now().Add(5*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != resumed.OperationID {
		t.Fatal("未领取恢复", err)
	}
	report := func(ctx context.Context, step projectrun.Step) error {
		allowed, err := database.CheckpointProjectRun(ctx, *lease, step)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("父租约失效")
		}
		return nil
	}
	facts, err := f.adapter.ResumeExisting(ctx, *lease, report)
	if err == nil || facts.Deployment != nil && facts.Deployment.Ready {
		t.Fatal("dirty恢复被错误接受")
	}
	if err := database.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "failed", Code: "DIRTY_PRESERVED", Message: "保留原迁移现场", Facts: &facts}); err != nil {
		t.Fatal(err)
	}
}
