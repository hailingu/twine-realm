// 本文件验证未知外部动作不会因父租约回收而重发。
package projectrun

import (
	"context"
	"testing"
)

// workerStore 只实现本测试实际消费的存储边界，意外调用会令测试失败。
type workerStore struct {
	Repository
	finished    Completion
	authorized  bool
	checkpoints []Step
}

// FinishProjectRun 捕获持久结算结果。
func (s *workerStore) FinishProjectRun(_ context.Context, _ Lease, value Completion) error {
	s.finished = value
	return nil
}

// ProjectRunAuthorized 返回测试明确授予的父执行许可。
func (s *workerStore) ProjectRunAuthorized(context.Context, Lease) (bool, error) {
	return s.authorized, nil
}

// CheckpointProjectRun 保存适配器先于动作提交的检查点。
func (s *workerStore) CheckpointProjectRun(_ context.Context, _ Lease, step Step) (bool, error) {
	s.checkpoints = append(s.checkpoints, step)
	return true, nil
}

// TestUnknownLeaseNeverExecutes 即使旧执行已过期也不能猜测外部动作已经撤销。
func TestUnknownLeaseNeverExecutes(t *testing.T) {
	for _, state := range []string{"executing", "unknown"} {
		t.Run(state, func(t *testing.T) {
			store := &workerStore{}
			worker := NewWorker(store, nil)
			err := worker.execute(context.Background(), Lease{Recover: true, Operation: Operation{Steps: []Step{{Name: "stop_application", State: state}}}})
			if err != nil {
				t.Fatal(err)
			}
			if store.finished.State != "needs_attention" || store.finished.Code != "PROJECT_RUN_UNKNOWN" {
				t.Fatal("未知动作未阻止恢复")
			}
		})
	}
}

// TestRevokedLeaseNeverExecutes 失去项目许可后不能进入任何适配器动作。
func TestRevokedLeaseNeverExecutes(t *testing.T) {
	store := &workerStore{}
	if err := NewWorker(store, nil).execute(context.Background(), Lease{}); err != nil {
		t.Fatal(err)
	}
	if store.finished.State != "" {
		t.Fatal("失效执行覆盖父状态")
	}
}

// interruptedAdapter 模拟平台只在确定步骤边界退出或命令在途退出。
type interruptedAdapter struct {
	Adapter
	cancel  context.CancelFunc
	pending bool
}

// StopExisting 通过真实检查点端口报告中断边界。
func (a *interruptedAdapter) StopExisting(ctx context.Context, _ Lease, checkpoint func(context.Context, Step) error) (Facts, error) {
	state := "succeeded"
	if a.pending {
		state = "executing"
	}
	if err := checkpoint(ctx, Step{Name: "stop_application", State: state}); err != nil {
		return Facts{}, err
	}
	a.cancel()
	return Facts{}, ctx.Err()
}

// TestInterruptedBoundary 检查只有明确完成边界可自动继续，在途动作保持未知。
func TestInterruptedBoundary(t *testing.T) {
	for _, pending := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		store := &workerStore{authorized: true}
		worker := NewWorker(store, &interruptedAdapter{cancel: cancel, pending: pending})
		err := worker.execute(ctx, Lease{Target: Target{Desired: "stopped"}})
		cancel()
		if err == nil {
			t.Fatal("未报告平台中断")
		}
		expected := "waiting_retry"
		if pending {
			expected = "needs_attention"
		}
		if store.finished.State != expected {
			t.Fatalf("中断结果=%s，期望%s", store.finished.State, expected)
		}
	}
}
