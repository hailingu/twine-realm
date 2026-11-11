//go:build integration

// 本文件以真实Todo和原物理资源验证持久父操作停止及保留数据恢复。
package httpapi_test

import (
	"context"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"reflect"
	"testing"
	"time"
)

// TestRealProjectRunPreservesCRUD 穿过真实受理、父队列、账本和严格Docker适配边界。
func TestRealProjectRunPreservesCRUD(t *testing.T) {
	f := openDeploymentFixture(t)
	_, accepted := f.accept(t, "")
	t.Cleanup(func() { cleanupDeploymentContainers(t, accepted.Target.Plan) })
	deployed, err := f.execute(t, accepted)
	if err != nil || deployed.Observation == nil || !deployed.Observation.Ready {
		t.Fatal("真实部署失败", err)
	}
	assertTodoCRUD(t, deployed.Observation.EntryURL)
	f.refreshDatabase(t)
	stopped := stopPhysicalProject(t, f)
	resumePhysicalProject(t, f, stopped)
}

// executePhysicalProjectRun 每步等待同库持久检查点，不以内存成功替代父操作结清。
func executePhysicalProjectRun(t *testing.T, f *deploymentFixture, receipt projectrun.Receipt) projectrun.Facts {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	database := f.runtime.base.database
	lease, err := database.ClaimProjectRun(ctx, time.Now(), time.Now().Add(5*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != receipt.OperationID {
		t.Fatal("未领取原父操作", err)
	}
	report := func(ctx context.Context, step projectrun.Step) error {
		allowed, err := database.CheckpointProjectRun(ctx, *lease, step)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("父操作已围栏")
		}
		return nil
	}
	var facts projectrun.Facts
	if receipt.Target.Desired == "stopped" {
		facts, err = f.adapter.StopExisting(ctx, *lease, report)
	} else {
		facts, err = f.adapter.ResumeExisting(ctx, *lease, report)
	}
	if err != nil {
		t.Fatal("真实父操作执行失败", err)
	}
	if err := database.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "succeeded", Facts: &facts}); err != nil {
		t.Fatal("父事实未结清", err)
	}
	return facts
}

// assertProjectMissingMaterialBlocks 真实移走自有原材料后，严格恢复必须在任何命令之前失败。
func assertProjectMissingMaterialBlocks(t *testing.T, f *deploymentFixture, stopped projectrun.Receipt) {
	t.Helper()
	restore := loseDeploymentMaterial(t, stopped.Target.Frozen.Deployment.Plan)
	defer restore()
	calls := 0
	_, err := f.adapter.ResumeExisting(context.Background(), projectrun.Lease{Target: stopped.Target}, func(context.Context, projectrun.Step) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatal("缺失原材料仍执行恢复", err, calls)
	}
	assertMaterialNotWritten(t, stopped.Target.Frozen.Deployment.Plan)
	fact, err := f.runtime.driver.Inspect(context.Background(), stopped.Target.Frozen.Runtime.Resource)
	if err != nil || fact == nil || fact.Running {
		t.Fatal("缺失材料核实改变数据库状态", err)
	}
}

// stopPhysicalProject 验证原键重放、实际停止及持久完成。
func stopPhysicalProject(t *testing.T, f *deploymentFixture) projectrun.Receipt {
	t.Helper()
	database := f.runtime.base.database
	service := projectrun.NewService(database, database, f.adapter)
	ctx := context.Background()
	command := projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "0", Desired: "stopped"}
	stopped, err := service.Accept(ctx, f.project.ID, command)
	if err != nil {
		t.Fatal("项目停止受理失败", err)
	}
	replayed, err := service.Accept(ctx, f.project.ID, command)
	if err != nil || !replayed.Replayed || replayed.OperationID != stopped.OperationID {
		t.Fatal("原键重复创建目标", err)
	}
	stopFacts := executePhysicalProjectRun(t, f, stopped)
	if !stopFacts.ApplicationStopped || !stopFacts.DatabaseStopped {
		t.Fatal("聚合停止未完成")
	}
	snapshot, err := database.ProjectRunSnapshot(ctx, f.project.ID)
	if err != nil || snapshot.Operation.State != "succeeded" {
		t.Fatal("停止事实未持久", err)
	}

	return stopped
}

// resumePhysicalProject 验证缺失材料负例后使用同一冻结计划恢复。
func resumePhysicalProject(t *testing.T, f *deploymentFixture, stopped projectrun.Receipt) {
	t.Helper()
	database := f.runtime.base.database
	service := projectrun.NewService(database, database, f.adapter)
	ctx := context.Background()
	assertProjectMissingMaterialBlocks(t, f, stopped)
	command := projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "1", Desired: "running"}
	resumed, err := service.Accept(ctx, f.project.ID, command)
	if err != nil {
		t.Fatal("原项目恢复受理失败", err)
	}
	if !reflect.DeepEqual(resumed.Target.Frozen, stopped.Target.Frozen) {
		t.Fatal("恢复替换了原冻结计划")
	}
	facts := executePhysicalProjectRun(t, f, resumed)
	assertRestoredProjectData(t, stopped, facts)

}

// assertRestoredProjectData 同时断言原身份、真实就绪与CRUD见证。
func assertRestoredProjectData(t *testing.T, stopped projectrun.Receipt, facts projectrun.Facts) {
	t.Helper()
	if facts.Runtime == nil || facts.Deployment == nil || !facts.Runtime.Ready || !facts.Deployment.Ready {
		t.Fatal("恢复缺少真实认证或HTTP就绪")
	}
	if facts.Runtime.ServerUUID != stopped.Target.Frozen.ServerUUID || facts.Runtime.ContainerID != stopped.Target.Frozen.DatabaseContainerID || facts.Deployment.ContainerID != stopped.Target.Frozen.ApplicationContainerID {
		t.Fatal("恢复改变原物理身份")
	}
	var todos []map[string]any
	businessRequest(t, facts.Deployment.EntryURL, "GET", "/api/todos", nil, 200, &todos)
	if len(todos) != 1 || todos[0]["title"] != "重启后保留" {
		t.Fatal("恢复丢失真实CRUD数据")
	}
}
