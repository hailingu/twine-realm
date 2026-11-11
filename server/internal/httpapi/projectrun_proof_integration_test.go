//go:build integration

// 本文件验证主动冻结证明与被动锁竞争诊断同时存在时的真实元数据库受理边界。
package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// acceptWithActiveProof 保留完整新证明，同时验证目标替换仍拒绝且不占用原请求键。
func acceptWithActiveProof(t *testing.T, f *deploymentFixture, original servicedeploy.Receipt, command projectrun.Command) projectrun.Receipt {
	t.Helper()
	ctx := context.Background()
	database := f.runtime.base.database
	runtime, err := database.RuntimeSnapshot(ctx, f.project.ID)
	if err != nil || runtime.Target == nil {
		t.Fatal(err)
	}
	frozen, proof, err := f.adapter.Freeze(ctx, *runtime.Target, original.Target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Project string
		Command projectrun.Command
	}{f.project.ID, command})
	if err != nil {
		t.Fatal(err)
	}
	input := projectrun.AcceptedInput{ProjectID: f.project.ID, Command: command, Frozen: frozen, Proof: proof, Hash: sha256.Sum256(data)}
	assertChangedProofRejected(t, f, input)
	markPassiveObservationUnknown(t, f)
	receipt, err := database.AcceptProjectRun(ctx, input)
	if err != nil {
		t.Fatal("完整本次证明被被动unknown否认", err)
	}
	replay, err := database.AcceptProjectRun(ctx, input)
	if err != nil || !replay.Replayed || replay.OperationID != receipt.OperationID {
		t.Fatal("同键重复受理丢失原证明回执", err)
	}
	return receipt
}

// assertChangedProofRejected 验证主动证明不能覆盖原目标身份。
func assertChangedProofRejected(t *testing.T, f *deploymentFixture, input projectrun.AcceptedInput) {
	t.Helper()
	ctx := context.Background()
	database := f.runtime.base.database
	changed := input
	changed.Frozen.Runtime.ID = id(t)
	if _, err := database.AcceptProjectRun(ctx, changed); err == nil {
		t.Fatal("目标改变后仍接受主动证明")
	} else {
		var fault *project.Error
		if !errors.As(err, &fault) || fault.Code != "PROJECT_RUN_TARGET_CHANGED" {
			t.Fatal("错误目标未按身份冲突拒绝", err)
		}
	}
}

// markPassiveObservationUnknown 仅修改测试自有被动诊断并核实未知读取。
func markPassiveObservationUnknown(t *testing.T, f *deploymentFixture) {
	t.Helper()
	ctx := context.Background()
	database := f.runtime.base.database
	// 模拟Freeze持flock期间普通观察失败，仅改本项目的被动诊断，不改主动证明。
	for _, table := range []string{"runtime_instances", "deployment_instances"} {
		if _, err := f.runtime.sql.Exec("UPDATE "+table+" SET observation_code='PROJECT_LOCK_BUSY' WHERE project_id=?", f.project.ID); err != nil {
			t.Fatal(err)
		}
	}
	current, err := database.RuntimeSnapshot(ctx, f.project.ID)
	if err != nil || current.Freshness != "unknown" {
		t.Fatal("未构造被动unknown边界", err)
	}
}
