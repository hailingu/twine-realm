//go:build integration

// 本文件用真实SQL验证关闭执行器后的原键恢复、有界事件、持久故障与失败结算回滚。
package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
)

// TestRuntimeDisabledKeepsOriginalHistory 能力关闭后不新受理，但原请求与操作仍可恢复查询。
func TestRuntimeDisabledKeepsOriginalHistory(t *testing.T) {
	f := openRuntimeFixture(t)
	accepted := f.accept(t, f.command)
	attachRuntime(t, f.base, nil)
	capability := decode[mysqlruntime.Capability](t, call(t, f.base.handler, "GET", "/api/v1/runtime/capabilities", nil), 200, "RuntimeCapability")
	if capability.Enabled {
		t.Fatal("未配置执行器仍宣称启用")
	}
	replayed := f.accept(t, f.command)
	if !replayed.Replayed || replayed.OperationID != accepted.OperationID {
		t.Fatal("关闭能力导致原键丢失或重受理")
	}
	next := f.command
	next.MutationID = id(t)
	next.BaseGeneration = "1"
	decode[any](t, call(t, f.base.handler, "POST", f.targetURL(), next), 503, "ErrorResponse")
	if value := f.snapshot(t); value.Generation != "1" {
		t.Fatal("关闭执行器后仍接受新目标")
	}
	for _, path := range []string{"/api/v1/projects/bad/runtime", "/api/v1/projects/" + f.current.ID + "/runtime/events?after=01", "/api/v1/projects/" + f.current.ID + "/runtime/operations/bad", "/api/v1/runtime-mutations/bad"} {
		decode[any](t, call(t, f.base.handler, "GET", path, nil), 400, "ErrorResponse")
	}
}

// TestRuntimeEventPaginationAndDurableFailure 验证故障、耗尽和未知步骤跨重开保留，持久事件可连续翻页。
func TestRuntimeEventPaginationAndDurableFailure(t *testing.T) {
	f := openRuntimeFixture(t)
	accepted := f.accept(t, f.command)
	lease := f.claim(t)
	if valid, err := f.base.database.CheckpointRuntime(context.Background(), *lease, mysqlruntime.Checkpoint{Name: "preflight", State: "executing"}); err != nil || !valid {
		t.Fatal("无法登记受控故障前置")
	}
	failure := mysqlruntime.Completion{State: "waiting_retry", Code: "DOCKER_UNAVAILABLE", StepCode: "DOCKER_UNAVAILABLE", Message: "受控Docker离线", NextAttempt: time.Now().UTC().Add(time.Minute)}
	if err := f.base.database.FinishRuntime(context.Background(), *lease, failure); err != nil {
		t.Fatal(err)
	}
	if next, err := f.base.database.ClaimRuntime(context.Background(), time.Now(), time.Now().Add(time.Minute)); err != nil || next != nil {
		t.Fatal("尚未到期的重试被提前领取")
	}
	if err := f.base.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.base.reopen(t)
	attachRuntime(t, f.base, f.driver)
	value := f.snapshot(t)
	if value.Operation.State != "waiting_retry" || value.Operation.Code != failure.Code || value.Freshness != "unknown" {
		t.Fatal("重开后丢失持久故障诊断")
	}
	if value.Operation.Steps[0].Name != "preflight" || value.Operation.Steps[0].State != "unknown" || value.Operation.Steps[0].Code != failure.Code {
		t.Fatal("步骤次序或未知原因丢失")
	}
	assertRuntimeEventPages(t, f, accepted.Target)
	if _, err := f.sql.Exec("UPDATE runtime_operations SET attempts=6,next_attempt=? WHERE id=?", time.Now().Add(-time.Second), accepted.OperationID); err != nil {
		t.Fatal(err)
	}
	if next, err := f.base.database.ClaimRuntime(context.Background(), time.Now(), time.Now().Add(time.Minute)); err != nil || next != nil {
		t.Fatal("耗尽操作继续执行")
	}
	value = f.snapshot(t)
	if value.Operation.State != "needs_attention" || value.Operation.Code != "RETRY_EXHAUSTED" || value.Operation.Attempts != 6 {
		t.Fatal("重试耗尽未持久化")
	}
}

// assertRuntimeEventPages 真实SQL变化产生事件，固定游标翻页不能丢失或重复项目序号。
func assertRuntimeEventPages(t *testing.T, f *runtimeFixture, target mysqlruntime.Target) {
	t.Helper()
	now := time.Now().UTC()
	for i := range 101 {
		code := "DOCKER_UNAVAILABLE"
		if i%2 == 1 {
			code = "RUNTIME_CHECKPOINT_UNCONFIRMED"
		}
		if err := f.base.database.ObserveRuntime(context.Background(), captureRuntimeTarget(t, f.base.database, target), nil, code, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/projects/" + f.current.ID + "/runtime/events?after="
	first := decode[mysqlruntime.EventPage](t, call(t, f.base.handler, "GET", path+"0", nil), 200, "RuntimeEventPage")
	if len(first.Events) != 100 || !first.HasMore || first.NextSequence != first.Events[99].Sequence {
		t.Fatal("运行事件首屏没有按有界游标返回")
	}
	second := decode[mysqlruntime.EventPage](t, call(t, f.base.handler, "GET", path+first.NextSequence, nil), 200, "RuntimeEventPage")
	if len(second.Events) == 0 || second.HasMore || second.Events[0].Sequence == first.NextSequence {
		t.Fatal("运行事件翻页丢失或重复")
	}
}

// TestRuntimeCannotCommitStaleSuccess 过期、未来、未认证或缺少数据卷事实都不能结算为成功。
func TestRuntimeCannotCommitStaleSuccess(t *testing.T) {
	f := openRuntimeFixture(t)
	f.accept(t, f.command)
	lease := f.claim(t)
	before := f.snapshot(t)
	valid := mysqlruntime.Observation{State: "running", Running: true, Ready: true, VolumeFound: true, ServerUUID: id(t), ContainerID: strings.Repeat("b", 64), ObservedAt: mysqlruntime.Timestamp(time.Now())}
	for _, change := range []func(*mysqlruntime.Observation){
		func(fact *mysqlruntime.Observation) {
			fact.ObservedAt = mysqlruntime.Timestamp(time.Now().Add(-time.Minute))
		},
		func(fact *mysqlruntime.Observation) {
			fact.ObservedAt = mysqlruntime.Timestamp(time.Now().Add(time.Minute))
		},
		func(fact *mysqlruntime.Observation) { fact.Ready = false },
		func(fact *mysqlruntime.Observation) { fact.VolumeFound = false },
	} {
		fact := valid
		change(&fact)
		if err := f.base.database.FinishRuntime(context.Background(), *lease, mysqlruntime.Completion{State: "succeeded", Code: "READY", Observation: &fact, NextAttempt: time.Now()}); err == nil {
			t.Fatal("不充分事实被持久化为成功")
		}
		after := f.snapshot(t)
		if after.Sequence != before.Sequence || after.Operation.Version != before.Operation.Version {
			t.Fatal("失败结算部分推进操作或事件")
		}
	}
	valid.ObservedAt = mysqlruntime.Timestamp(time.Now())
	if err := f.base.database.FinishRuntime(context.Background(), *lease, mysqlruntime.Completion{State: "succeeded", Code: "READY", Observation: &valid, NextAttempt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if value := f.snapshot(t); value.Operation.State != "succeeded" || value.Observation.ServerUUID != valid.ServerUUID {
		t.Fatal("合法近期事实无法完整结算")
	}
}
