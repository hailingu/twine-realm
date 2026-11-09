// 本文件验证父账本在原命令执行前后失效时保守保留未知结果。
package servicecompose

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"testing"
)

// TestExistingActionLedger 确认未知、已完成、外部失败与检查点失败均不产生盲重试。
func TestExistingActionLedger(t *testing.T) {
	for _, test := range []struct {
		name, state string
		needed      bool
		calls       int
		wantErr     bool
	}{
		{"首次执行", "", true, 1, false}, {"无需动作", "", false, 0, false}, {"已完成边界", "succeeded", false, 0, false}, {"完成后外部漂移", "succeeded", true, 0, true}, {"执行未知", "executing", true, 0, true}, {"明确未知", "unknown", true, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lease := projectrun.Lease{}
			if test.state != "" {
				lease.Operation.Steps = []projectrun.Step{{Name: "stop", State: test.state}}
			}
			calls := 0
			err := existingAction(context.Background(), lease, "stop", "physical", test.needed, func(context.Context, projectrun.Step) error { return nil }, func() error { calls++; return nil })
			if (err != nil) != test.wantErr || calls != test.calls {
				t.Fatalf("账本行为错误 calls=%d err=%v", calls, err)
			}
		})
	}
}

// TestExistingActionFailureOwnership 外部失败登记未知，前置检查点失败绝不执行动作。
func TestExistingActionFailureOwnership(t *testing.T) {
	failed := errors.New("测试失败")
	for _, before := range []bool{true, false} {
		calls := 0
		states := []string{}
		err := existingAction(context.Background(), projectrun.Lease{}, "stop", "physical", true, func(_ context.Context, s projectrun.Step) error {
			states = append(states, s.State)
			if before {
				return failed
			}
			return nil
		}, func() error { calls++; return failed })
		if err == nil {
			t.Fatal("失败未传播")
		}
		if before && calls != 0 {
			t.Fatal("检查点失败仍执行命令")
		}
		if !before && (calls != 1 || len(states) != 2 || states[1] != "unknown") {
			t.Fatal("外部失败未登记未知", states)
		}
	}
}

// TestExistingActionLaterUnknown 不允许较早的成功记录遮蔽后面的未知外部命令。
func TestExistingActionLaterUnknown(t *testing.T) {
	lease := projectrun.Lease{Operation: projectrun.Operation{Steps: []projectrun.Step{{Name: "stop", State: "succeeded"}, {Name: "other", State: "unknown"}}}}
	calls := 0
	err := existingAction(context.Background(), lease, "stop", "physical", false, func(context.Context, projectrun.Step) error { calls++; return nil }, func() error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatal("后续未知动作被成功记录遮蔽", err, calls)
	}
}

// TestExistingActionCompletionFailure 外部成功但提交失败仍返回失败，不重复执行。
func TestExistingActionCompletionFailure(t *testing.T) {
	failed := errors.New("完成回执失败")
	calls := 0
	err := existingAction(context.Background(), projectrun.Lease{}, "stop", "physical", true, func(_ context.Context, s projectrun.Step) error {
		if s.State == "succeeded" {
			return failed
		}
		return nil
	}, func() error { calls++; return nil })
	if !errors.Is(err, failed) || calls != 1 {
		t.Fatal("完成提交失败被隐藏", err, calls)
	}
}

// TestExistingActionUnknownWriteFailure 外部与未知账本双失败均保留，不把任何一项吞掉。
func TestExistingActionUnknownWriteFailure(t *testing.T) {
	external := errors.New("外部失败")
	checkpoint := errors.New("未知回执失败")
	err := existingAction(context.Background(), projectrun.Lease{}, "stop", "physical", true, func(_ context.Context, s projectrun.Step) error {
		if s.State == "unknown" {
			return checkpoint
		}
		return nil
	}, func() error { return external })
	if !errors.Is(err, external) || !errors.Is(err, checkpoint) {
		t.Fatal("双失败未保留", err)
	}
}
