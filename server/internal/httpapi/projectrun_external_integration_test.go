//go:build integration

// 本文件只改变自有测试容器，证明外部删除及晚到停止不能绕过持久父账本。
package httpapi_test

import (
	"context"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"testing"
	"time"
)

// TestRealProjectRunLateStopRemainsUnknown 原Docker停止超时后继续完成，仍不得解锁反向恢复。
func TestRealProjectRunLateStopRemainsUnknown(t *testing.T) {
	f, receipt := externalProjectFixture(t)
	database := f.runtime.base.database
	ctx := context.Background()
	lease, err := database.ClaimProjectRun(ctx, time.Now(), time.Now().Add(5*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != receipt.OperationID {
		t.Fatal("原父操作未领取", err)
	}
	frozen := receipt.Target.Frozen
	if _, err := deploymentDocker(ctx, frozen.Deployment.Plan, "kill", "--signal", "STOP", frozen.ApplicationContainerID); err != nil {
		t.Fatal("测试原业务进程未挂起", err)
	}
	interruptOriginalStop(t, f, *lease)
	if err := database.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "needs_attention", Code: "PROJECT_EXTERNAL_UNKNOWN", Message: "测试原停止命令未知"}); err != nil {
		t.Fatal(err)
	}
	waitExternalApplicationStopped(t, f, receipt)
	state, err := database.ProjectRunSnapshot(ctx, f.project.ID)
	if err != nil || state.Operation.State != "needs_attention" {
		t.Fatal("外部晚到成功抹除了持久未知", err)
	}
	service := projectrun.NewService(database, database, f.adapter)
	_, err = service.Accept(ctx, f.project.ID, projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "1", Desired: "running"})
	if err == nil {
		t.Fatal("一次停止观测解锁了未知原命令的反向恢复")
	}
}

// externalProjectFixture 仅为此用例部署独立已验证业务并冻结原物理目标。
func externalProjectFixture(t *testing.T) (*deploymentFixture, projectrun.Receipt) {
	t.Helper()
	f := openDeploymentFixture(t)
	_, accepted := f.accept(t, "")
	t.Cleanup(func() { cleanupDeploymentContainers(t, accepted.Target.Plan) })
	result, err := f.execute(t, accepted)
	if err != nil || result.Observation == nil || !result.Observation.Ready {
		t.Fatal("测试业务未就绪", err)
	}
	f.refreshDatabase(t)
	service := projectrun.NewService(f.runtime.base.database, f.runtime.base.database, f.adapter)
	receipt, err := service.Accept(context.Background(), f.project.ID, projectrun.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "0", Desired: "stopped"})
	if err != nil {
		t.Fatal("停止目标未冻结", err)
	}
	return f, receipt
}

// waitExternalApplicationStopped 仅观察同一测试容器，不发第二次停止或修复命令。
func waitExternalApplicationStopped(t *testing.T, f *deploymentFixture, receipt projectrun.Receipt) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := deploymentDocker(ctx, receipt.Target.Frozen.Deployment.Plan, "inspect", "--format={{.State.Running}}", receipt.Target.Frozen.ApplicationContainerID)
		if err == nil && string(data) == "false\n" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("原停止命令未在观察期限内完成", err)
		case <-ticker.C:
		}
	}
}

// TestRealProjectRunDeletedApplicationFailsClosed 原应用删除后不重建容器，不启动数据库。
func TestRealProjectRunDeletedApplicationFailsClosed(t *testing.T) {
	f, receipt := externalProjectFixture(t)
	executePhysicalProjectRun(t, f, receipt)
	frozen := receipt.Target.Frozen
	assertExternalRenameBlocks(t, f, receipt)
	if _, err := deploymentDocker(context.Background(), frozen.Deployment.Plan, "rm", frozen.ApplicationContainerID); err != nil {
		t.Fatal("测试原应用未删除", err)
	}
	calls := 0
	_, err := f.adapter.ResumeExisting(context.Background(), projectrun.Lease{Target: receipt.Target}, func(context.Context, projectrun.Step) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatal("缺失原应用仍执行恢复", err, calls)
	}
	fact, err := f.runtime.driver.Inspect(context.Background(), frozen.Runtime.Resource)
	if err != nil || fact == nil || fact.Running {
		t.Fatal("缺失原应用导致数据库启动", err)
	}
}

// assertExternalRenameBlocks 原容器被外部改名后不创建同名替代，恢复名称仍保持同一ID。
func assertExternalRenameBlocks(t *testing.T, f *deploymentFixture, receipt projectrun.Receipt) {
	t.Helper()
	frozen := receipt.Target.Frozen
	original := frozen.Deployment.Plan.Resources.Application
	if _, err := deploymentDocker(context.Background(), frozen.Deployment.Plan, "rename", frozen.ApplicationContainerID, original+"-external"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := deploymentDocker(context.Background(), frozen.Deployment.Plan, "rename", frozen.ApplicationContainerID, original); err != nil {
			t.Error("测试原名称恢复失败", err)
		}
	}()
	calls := 0
	_, err := f.adapter.ResumeExisting(context.Background(), projectrun.Lease{Target: receipt.Target}, func(context.Context, projectrun.Step) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatal("外部改名仍执行恢复", err, calls)
	}
}

// interruptOriginalStop 仅在执行意图已持久后取消原命令，并断言未知结果。
func interruptOriginalStop(t *testing.T, f *deploymentFixture, lease projectrun.Lease) {
	t.Helper()
	ctx := context.Background()
	database := f.runtime.base.database
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	sent := false
	_, err := f.adapter.StopExisting(work, lease, func(c context.Context, step projectrun.Step) error {
		allowed, err := database.CheckpointProjectRun(c, lease, step)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("原父操作围栏失效")
		}
		if step.Name == "stop_application" && step.State == "executing" {
			sent = true
			time.AfterFunc(750*time.Millisecond, cancel)
		}
		return nil
	})
	if err == nil || !sent {
		t.Fatal("未产生真实在途停止未知", err)
	}
}
