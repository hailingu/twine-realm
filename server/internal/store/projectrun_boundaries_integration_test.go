//go:build integration

// 本文件以真实元数据库覆盖原键、版本、租约和未知恢复的拒绝边界。
package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// seedProjectRunReceipt 为账本夹具补齐独立原键记录，不绕过被测受理路径。
func seedProjectRunReceipt(t *testing.T, s *Store, receipt projectrun.Receipt) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	hash := [32]byte{}
	if _, err := s.db.Exec("INSERT INTO projectrun_receipts(id,intent_hash,response,created_at) VALUES(?,?,?,?)", receipt.MutationID, hash[:], data, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// setTestProjectRunState 只改变测试自有操作状态，制造真实持久恢复边界。
func setTestProjectRunState(t *testing.T, s *Store, receipt projectrun.Receipt, state string) {
	t.Helper()
	if _, err := s.db.Exec("UPDATE projectrun_operations SET payload=JSON_SET(payload,'$.state',?) WHERE id=?", state, receipt.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE projectrun_instances SET state=? WHERE project_id=?", state, receipt.Target.ProjectID); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunAcceptanceVersionGuards 原键冲突和过期版本不能占用新请求回执。
func TestProjectRunAcceptanceVersionGuards(t *testing.T) {
	s, receipt := projectRunFixture(t)
	seedProjectRunReceipt(t, s, receipt)
	ctx := context.Background()
	input := projectrun.AcceptedInput{ProjectID: receipt.Target.ProjectID, Command: projectrun.Command{MutationID: receipt.MutationID}}
	input.Hash[0] = 1
	if _, err := s.AcceptProjectRun(ctx, input); err == nil {
		t.Fatal("同键不同内容仍受理")
	}
	input.Hash[0] = 0
	replay, err := s.AcceptProjectRun(ctx, input)
	if err != nil || !replay.Replayed {
		t.Fatal("同键重放失败", err)
	}
	for _, version := range []struct{ revision, generation string }{{"999", "1"}, {"0", "999"}} {
		input.Command = projectrun.Command{MutationID: testProjectRunID(t), BaseRevision: version.revision, BaseGeneration: version.generation, Desired: "stopped"}
		if _, err := s.AcceptProjectRun(ctx, input); err == nil {
			t.Fatal("过期版本仍受理")
		}
		if _, _, err := s.ProjectRunReceipt(ctx, input.Command.MutationID); err == nil {
			t.Fatal("拒绝请求留下回执")
		}
	}
}

// TestProjectRunExplicitRetryGuards 已知失败可按原键恢复，未知或非当前操作不能重开。
func TestProjectRunExplicitRetryGuards(t *testing.T) {
	s, receipt := projectRunFixture(t)
	seedProjectRunReceipt(t, s, receipt)
	ctx := context.Background()
	setTestProjectRunState(t, s, receipt, "failed")
	value, err := s.RetryProjectRun(ctx, receipt.Target.ProjectID, receipt.MutationID)
	if err != nil || value.OperationID != receipt.OperationID {
		t.Fatal("已知失败未恢复原操作", err)
	}
	replay, err := s.RetryProjectRun(ctx, receipt.Target.ProjectID, receipt.MutationID)
	if err != nil || !replay.Replayed {
		t.Fatal("重试重复请求非幂等", err)
	}
	for _, state := range []string{"needs_attention", "succeeded"} {
		setTestProjectRunState(t, s, receipt, state)
		if _, err := s.RetryProjectRun(ctx, receipt.Target.ProjectID, receipt.MutationID); err == nil {
			t.Fatal("不安全终态被重开")
		}
	}
	if _, err := s.RetryProjectRun(ctx, receipt.Target.ProjectID, testProjectRunID(t)); err == nil {
		t.Fatal("缺少原键仍重试")
	}
}

// TestProjectRunLeaseAndInterruptedGuards 过期或错误执行无权结算，已登记动作中断永久保留未知。
func TestProjectRunLeaseAndInterruptedGuards(t *testing.T) {
	s, receipt := projectRunFixture(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.ClaimProjectRun(ctx, now, now); err == nil {
		t.Fatal("无效租约被接受")
	}
	lease, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	invalid := *lease
	invalid.Execution++
	if ok, err := s.ProjectRunAuthorized(ctx, invalid); err != nil || ok {
		t.Fatal("错误执行获许可")
	}
	if err := s.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "invalid"}); err == nil {
		t.Fatal("非法完成未拒绝")
	}
	point := projectrun.Step{Name: "stop_application", State: "executing", ExternalID: "original"}
	if ok, err := s.CheckpointProjectRun(ctx, *lease, point); err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.YieldProjectRun(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	value, err := s.ProjectRunSnapshot(ctx, receipt.Target.ProjectID)
	if err != nil || value.Operation.State != "needs_attention" || value.Operation.Steps[0].State != "unknown" {
		t.Fatal("中断未知丢失", err)
	}
}

// TestProjectRunReceiptCorruption 回执占位及损坏载荷不能冒充可恢复结果。
func TestProjectRunReceiptCorruption(t *testing.T) {
	s, receipt := projectRunFixture(t)
	seedProjectRunReceipt(t, s, receipt)
	ctx := context.Background()
	for _, payload := range []any{nil, `"damaged"`} {
		if _, err := s.db.Exec("UPDATE projectrun_receipts SET response=? WHERE id=?", payload, receipt.MutationID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ProjectRunReceipt(ctx, receipt.MutationID); err == nil {
			t.Fatal("损坏回执被接受")
		}
	}
	for _, column := range []string{"target_json", "payload"} {
		if _, err := s.db.Exec("UPDATE projectrun_operations SET "+column+"=? WHERE id=?", `"damaged"`, receipt.OperationID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProjectRunSnapshot(ctx, receipt.Target.ProjectID); err == nil {
			t.Fatal("损坏父记录被接受")
		}
		if _, err := s.ClaimProjectRun(ctx, time.Now(), time.Now().Add(time.Minute)); err == nil {
			t.Fatal("损坏父记录仍可领取")
		}
		if column == "target_json" {
			restoreTestProjectRunTarget(t, s, receipt)
		}
	}
}

// testProjectRunID 为本测试创建独立请求身份。
func testProjectRunID(t *testing.T) string {
	t.Helper()
	id, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// restoreTestProjectRunTarget 只恢复测试自有载荷，以独立验证下一种损坏边界。
func restoreTestProjectRunTarget(t *testing.T, s *Store, receipt projectrun.Receipt) {
	t.Helper()
	data, err := json.Marshal(receipt.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE projectrun_operations SET target_json=? WHERE id=?", data, receipt.OperationID); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunKnownRetryDelay 已知取消等待固定间隔，旧执行不能借自动重领继续写入。
func TestProjectRunKnownRetryDelay(t *testing.T) {
	s, _ := projectRunFixture(t)
	ctx := context.Background()
	now := time.Now()
	lease, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err := s.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "waiting_retry", Code: "INTERRUPTED"}); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || next != nil {
		t.Fatal("等待间隔内重新执行", err)
	}
	later := now.Add(time.Minute)
	next, err = s.ClaimProjectRun(ctx, later, later.Add(time.Minute))
	if err != nil || next == nil || !next.Recover {
		t.Fatal("已知边界未接续", err)
	}
	if err := s.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ProjectRunAuthorized(ctx, *next); err != nil || !ok {
		t.Fatal("旧执行影响新租约", err)
	}
}

// TestProjectRunCrossProjectRetry 原键不能跨项目使用，失去当前操作归属也不能重开。
func TestProjectRunCrossProjectRetry(t *testing.T) {
	s, receipt := projectRunFixture(t)
	seedProjectRunReceipt(t, s, receipt)
	ctx := context.Background()
	key := testProjectRunID(t)
	other, err := s.Create(ctx, project.CreateCommand{MutationID: key, Name: "独立重试归属"}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryProjectRun(ctx, other.Project.ID, receipt.MutationID); err == nil {
		t.Fatal("原键跨项目使用")
	}
	receipt.OperationID = testProjectRunID(t)
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE projectrun_receipts SET response=? WHERE id=?", data, receipt.MutationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryProjectRun(ctx, receipt.Target.ProjectID, receipt.MutationID); err == nil {
		t.Fatal("非当前操作重新开始")
	}
}
