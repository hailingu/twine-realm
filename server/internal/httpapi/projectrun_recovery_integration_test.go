//go:build integration

// 本文件验证真实应用停止边界的平台重启及另一运行项目隔离。
package httpapi_test

import (
	"context"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"testing"
	"time"
)

// prepareRunningProject 为隔离验收创建真实服务与一条持久业务数据。
func prepareRunningProject(t *testing.T) (*deploymentFixture, servicedeploy.Receipt) {
	t.Helper()
	f := openDeploymentFixture(t)
	_, receipt := f.accept(t, "")
	t.Cleanup(func() { cleanupDeploymentContainers(t, receipt.Target.Plan) })
	result, err := f.execute(t, receipt)
	if err != nil || result.Observation == nil || !result.Observation.Ready {
		t.Fatal("真实项目未就绪", err)
	}
	assertTodoCRUD(t, result.Observation.EntryURL)
	return f, receipt
}

// TestRealProjectRunRestartAndIsolation 重建元DB及适配器后只续跑原持久步骤，另一项目原数据继续可用。
func TestRealProjectRunRestartAndIsolation(t *testing.T) {
	f, original := prepareRunningProject(t)
	other, otherReceipt := prepareRunningProject(t)
	otherFact, err := other.adapter.Inspect(context.Background(), otherReceipt.Target)
	if err != nil || otherFact == nil || !otherFact.Ready {
		t.Fatal(err)
	}
	f.refreshDatabase(t)
	// 第二个项目构建期间原服务观察可能过期，重新采集真实原事实。
	fresh, err := f.adapter.Inspect(context.Background(), original.Target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.base.database.ObserveDeployment(context.Background(), captureDeploymentTarget(t, f.runtime.base.database, original.Target), fresh, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	database := f.runtime.base.database
	service := projectrun.NewService(database, database, f.adapter)
	command := projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "0", Desired: "stopped"}
	stopped := acceptWithActiveProof(t, f, original, command)
	interruptAfterApplication(t, f, stopped)
	f.reopen(t)
	database = f.runtime.base.database
	service = projectrun.NewService(database, database, f.adapter)
	replay, err := service.Accept(context.Background(), f.project.ID, command)
	if err != nil || !replay.Replayed || replay.OperationID != stopped.OperationID {
		t.Fatal("重启丢失原请求", err)
	}
	executePhysicalProjectRun(t, f, stopped)
	assertOtherProjectUnchanged(t, other, otherReceipt, otherFact.ContainerID, otherFact.DatabaseUUID)
	resumed, err := service.Accept(context.Background(), f.project.ID, projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "1", Desired: "running"})
	if err != nil {
		t.Fatal(err)
	}
	facts := executePhysicalProjectRun(t, f, resumed)
	if facts.Runtime.ServerUUID != stopped.Target.Frozen.ServerUUID || facts.Deployment.ContainerID != stopped.Target.Frozen.ApplicationContainerID {
		t.Fatal("重启替换原身份")
	}
	var todos []map[string]any
	businessRequest(t, facts.Deployment.EntryURL, "GET", "/api/todos", nil, 200, &todos)
	if len(todos) != 1 || todos[0]["title"] != "重启后保留" {
		t.Fatal("重启接续丢失原数据")
	}
	assertOtherProjectUnchanged(t, other, otherReceipt, otherFact.ContainerID, otherFact.DatabaseUUID)
}

// interruptAfterApplication 真实停止应用并持久提交后中断，再释放已知边界供重启领取。
func interruptAfterApplication(t *testing.T, f *deploymentFixture, receipt projectrun.Receipt) {
	t.Helper()
	database := f.runtime.base.database
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease, err := database.ClaimProjectRun(ctx, time.Now(), time.Now().Add(5*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != receipt.OperationID {
		t.Fatal("未领取本次停止", err)
	}
	report := func(ctx context.Context, step projectrun.Step) error {
		allowed, err := database.CheckpointProjectRun(ctx, *lease, step)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("父操作失效")
		}
		if step.Name == "stop_application" && step.State == "succeeded" {
			cancel()
			return context.Canceled
		}
		return nil
	}
	if _, err := f.adapter.StopExisting(ctx, *lease, report); err == nil {
		t.Fatal("边界取消未中断")
	}
	if err := database.YieldProjectRun(context.Background(), *lease); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.ProjectRunSnapshot(context.Background(), f.project.ID)
	if err != nil || snapshot.Operation.State != "accepted" || len(snapshot.Operation.Steps) != 1 || snapshot.Operation.Steps[0].State != "succeeded" {
		t.Fatal("已知边界未持久", err)
	}
	fact, err := f.runtime.driver.Inspect(context.Background(), receipt.Target.Frozen.Runtime.Resource)
	if err != nil || fact == nil || !fact.Running {
		t.Fatal("应用边界已越过数据库停止", err)
	}
}

// assertOtherProjectUnchanged 重新认证另一原数据库并验证真实业务记录。
func assertOtherProjectUnchanged(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt, container, uuid string) {
	t.Helper()
	fact, err := f.adapter.Inspect(context.Background(), receipt.Target)
	if err != nil || fact == nil || !fact.Ready || fact.ContainerID != container || fact.DatabaseUUID != uuid {
		t.Fatal("其他原项目受影响", err)
	}
	var todos []map[string]any
	businessRequest(t, fact.EntryURL, "GET", "/api/todos", nil, 200, &todos)
	if len(todos) != 1 || todos[0]["title"] != "重启后保留" {
		t.Fatal("其他项目数据改变")
	}
}
