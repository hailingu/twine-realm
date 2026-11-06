//go:build integration

// 本文件证明首次材料尚未生成不能锁死原数据库，已生成后丢失仍须保留并阻塞。
package httpapi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// TestRealDeploymentBeforeMaterialDoesNotBlockDatabase 真实候选tag暂时丢失导致早期失败，原卷/UUID/数据仍能停止恢复。
func TestRealDeploymentBeforeMaterialDoesNotBlockDatabase(t *testing.T) {
	f := openDeploymentFixture(t)
	_, accepted := f.accept(t, "")
	plan := accepted.Target.Plan
	t.Cleanup(func() { cleanupDeploymentContainers(t, plan) })
	assertMaterialCompletionProof(t, f, false)
	deploymentSQL(t, plan, "CREATE DATABASE deployment_recovery_witness", "")
	deploymentSQL(t, plan, "CREATE TABLE deployment_recovery_witness.saved (value VARCHAR(16))", "")
	deploymentSQL(t, plan, "INSERT INTO deployment_recovery_witness.saved VALUES('kept')", "")
	restore := hideDeploymentCandidateTag(t, f, plan)
	result, executeErr := f.execute(t, accepted)
	restore()
	if executeErr == nil || result.State == "succeeded" {
		t.Fatal("候选缺失未在部署预检阻断")
	}
	assertMaterialNotWritten(t, plan)
	changeDeploymentDatabaseGoal(t, f, "stopped")
	fact := changeDeploymentDatabaseGoal(t, f, "running")
	if !fact.Ready || fact.ServerUUID != plan.Database.ServerUUID {
		t.Fatal("恢复没有沿用原认证数据库身份")
	}
	deploymentSQL(t, plan, "SELECT value FROM deployment_recovery_witness.saved", "kept")
	f.request.BaseGeneration = accepted.Target.Generation
	_, resumed := f.accept(t, accepted.OperationID)
	if _, err := f.execute(t, resumed); err != nil {
		t.Fatal("原数据库恢复后无法按原计划继续部署", err)
	}
	assertTodoCRUD(t, "http://127.0.0.1:"+strconv.Itoa(plan.Request.HostPort))
	assertCompletedMaterialLoss(t, f, resumed)
}

// assertMaterialCompletionProof 原目标和不可变事件在同一读屏障内核实，不能从本次pending猜测旧材料不存在。
func assertMaterialCompletionProof(t *testing.T, f *deploymentFixture, expected bool) {
	t.Helper()
	value, err := f.runtime.base.database.RuntimeDeployment(context.Background(), f.project.ID)
	if err != nil || value == nil || value.Target.Plan.RuntimeDigest == "" || value.Materialized != expected {
		t.Fatal("材料完成证明丢失或错误授予", expected, err)
	}
}

// assertCompletedMaterialLoss 新操作预检失败也保留既有证明；丢失材料必须先明确恢复原文件。
func assertCompletedMaterialLoss(t *testing.T, f *deploymentFixture, previous servicedeploy.Receipt) {
	t.Helper()
	f.request.BaseGeneration = previous.Target.Generation
	_, failed := f.accept(t, previous.OperationID)
	restoreImage := hideDeploymentCandidateTag(t, f, failed.Target.Plan)
	_, err := f.execute(t, failed)
	restoreImage()
	if err == nil {
		t.Fatal("再次预检未失败")
	}
	assertMaterialCompletionProof(t, f, true)
	changeDeploymentDatabaseGoal(t, f, "stopped")
	restoreMaterial := loseDeploymentMaterial(t, failed.Target.Plan)
	assertMissingCompletedMaterialBlocks(t, f)
	assertMaterialNotWritten(t, failed.Target.Plan)
	f.reopen(t)
	assertMaterialCompletionProof(t, f, true)
	restoreMaterial()
	changeDeploymentDatabaseGoal(t, f, "running")
	f.request.BaseGeneration = failed.Target.Generation
	_, restored := f.accept(t, failed.OperationID)
	result, err := f.execute(t, restored)
	if err != nil || result.Observation == nil {
		t.Fatal("原材料明确恢复后无法继续", err)
	}
	assertDeploymentRecoveredData(t, restored.Target.Plan, result.Observation)
}

// loseDeploymentMaterial 仅移走本次确定文件，清理时恢复同一文件，不生成替代材料。
func loseDeploymentMaterial(t *testing.T, plan servicedeploy.Plan) func() {
	t.Helper()
	name := filepath.Join(os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY"), "projects", plan.Database.ResourceID, "services", plan.ServiceID, "compose.json")
	backup := name + ".review-backup"
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("本次材料备份路径并非全新", err)
	}
	if err := os.Rename(name, backup); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if !restored {
			if err := os.Rename(backup, name); err != nil {
				t.Error("本次原材料未恢复", err)
				return
			}
			restored = true
		}
	}
	t.Cleanup(restore)
	return restore
}

// assertMissingCompletedMaterialBlocks 原入口持久保存失败，不以手工启动或静默补写绕过原材料。
func assertMissingCompletedMaterialBlocks(t *testing.T, f *deploymentFixture) {
	t.Helper()
	value := f.runtime.snapshot(t)
	f.runtime.accept(t, mysqlruntime.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: value.Generation, Desired: "running", RetryOf: value.Operation.ID})
	lease := f.runtime.claim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	fact, err := f.runtime.driver.Execute(ctx, *lease, 120*time.Second, func(ctx context.Context, point mysqlruntime.Checkpoint) (bool, error) {
		return f.runtime.base.database.CheckpointRuntime(ctx, *lease, point)
	})
	code, message, _ := servicedeploy.Diagnostic(err)
	if err == nil || code != "DEPLOY_MATERIAL_CONFLICT" || fact != nil && fact.Running {
		t.Fatal("已生成材料丢失被错误地忽略", code)
	}
	if err := f.runtime.base.database.FinishRuntime(ctx, *lease, mysqlruntime.Completion{State: "failed", Code: code, Message: message, NextAttempt: time.Now().UTC(), Observation: fact}); err != nil {
		t.Fatal(err)
	}
}

// hideDeploymentCandidateTag 仅改动本测试拥有的候选别名，保留原内容并在退出前恢复，不强删镜像。
func hideDeploymentCandidateTag(t *testing.T, f *deploymentFixture, plan servicedeploy.Plan) func() {
	t.Helper()
	image := f.candidate.Image
	backup := image.Tag + "-review-backup"
	if _, err := deploymentDocker(context.Background(), plan, "image", "tag", image.ID, backup); err != nil {
		t.Fatal("本次候选保护别名未建立", err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if _, err := deploymentDocker(context.Background(), plan, "image", "tag", backup, image.Tag); err != nil {
			t.Error("本次原候选别名未恢复", err)
			return
		}
		if _, err := deploymentDocker(context.Background(), plan, "image", "rm", backup); err != nil {
			t.Error("本次候选临时别名未回收", err)
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	if _, err := deploymentDocker(context.Background(), plan, "image", "rm", image.Tag); err != nil {
		t.Fatal("本次预检失败未注入", err)
	}
	return restore
}

// assertMaterialNotWritten 核实早期失败的实际文件，不能用步骤标签猜测已创建。
func assertMaterialNotWritten(t *testing.T, plan servicedeploy.Plan) {
	t.Helper()
	name := filepath.Join(os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY"), "projects", plan.Database.ResourceID, "services", plan.ServiceID, "compose.json")
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("预检失败后出现业务Compose材料", err)
	}
}

// changeDeploymentDatabaseGoal 使用原公开受理入口和持久队列，不以手工docker start绕过待验证边界。
func changeDeploymentDatabaseGoal(t *testing.T, f *deploymentFixture, desired string) *mysqlruntime.Observation {
	t.Helper()
	value := f.runtime.snapshot(t)
	command := mysqlruntime.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: value.Generation, Desired: desired, RetryOf: value.Operation.ID}
	f.runtime.accept(t, command)
	lease := f.runtime.claim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	fact, err := f.runtime.driver.Execute(ctx, *lease, 120*time.Second, func(ctx context.Context, point mysqlruntime.Checkpoint) (bool, error) {
		return f.runtime.base.database.CheckpointRuntime(ctx, *lease, point)
	})
	if err != nil {
		t.Fatal("原运行入口不能完成数据库目标", desired, err)
	}
	if err := f.runtime.base.database.FinishRuntime(ctx, *lease, mysqlruntime.Completion{State: "succeeded", Code: "CONFIRMED", Message: "本次原数据库目标已核实", NextAttempt: time.Now().UTC(), Observation: fact}); err != nil {
		t.Fatal(err)
	}
	return fact
}
