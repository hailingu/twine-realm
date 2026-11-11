// 本文件覆盖外部失败分类，不以取消掩盖未确认动作。
package projectrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// TestExecutionDiagnostics 已知失败可显式重试，未知失败不能因并发平台退出变为安全。
func TestExecutionDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		steps    map[string]string
		cancel   bool
		expected string
	}{
		{"成功", nil, nil, false, "succeeded"},
		{"已知资源缺失", servicedeploy.Failure("MISSING", "原材料缺失", false), nil, false, "failed"},
		{"未知外部错误", errors.New("unknown"), nil, false, "needs_attention"},
		{"取消不消除未知", errors.New("unknown"), nil, true, "needs_attention"},
		{"已知失败仍有动作在途", servicedeploy.Failure("FAIL", "失败", false), map[string]string{"start": "executing"}, false, "needs_attention"},
		{"已知边界取消", context.Canceled, map[string]string{"stop": "succeeded"}, true, "waiting_retry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			got := executionResult(ctx, Facts{}, tc.err, tc.steps)
			if got.State != tc.expected {
				t.Fatalf("状态%s 期望%s", got.State, tc.expected)
			}
		})
	}
}

// failingQueue 只提供失败队列，确保工作器不会吞掉持久存储错误。
type failingQueue struct {
	Repository
	failure error
}

// ClaimProjectRun 返回明确的数据库失败。
func (q failingQueue) ClaimProjectRun(context.Context, time.Time, time.Time) (*Lease, error) {
	return nil, q.failure
}

// TestWorkerQueueFailure 数据库失败必须终止并报告，已取消平台直接返回。
func TestWorkerQueueFailure(t *testing.T) {
	failure := errors.New("数据库失败")
	if err := NewWorker(failingQueue{failure: failure}, nil).Run(context.Background()); !errors.Is(err, failure) {
		t.Fatal("队列错误被吞掉", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewWorker(nil, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
}

// rejectingStore 区分授权读取失败、检查点拒绝和写失败。
type rejectingStore struct {
	*workerStore
	authorizationError error
	checkpointError    error
	allow              bool
}

// ProjectRunAuthorized 模拟读取失败或正常许可。
func (s rejectingStore) ProjectRunAuthorized(context.Context, Lease) (bool, error) {
	return true, s.authorizationError
}

// CheckpointProjectRun 返回明确拒绝或无法确认的写结果。
func (s rejectingStore) CheckpointProjectRun(context.Context, Lease, Step) (bool, error) {
	return s.allow, s.checkpointError
}

// reportingAdapter 只尝试一个持久动作入口，不执行外部命令。
type reportingAdapter struct{ Adapter }

// ResumeExisting 将检查点错误返回工作器，不能直接报告成功。
func (reportingAdapter) ResumeExisting(ctx context.Context, _ Lease, report func(context.Context, Step) error) (Facts, error) {
	return Facts{}, report(ctx, Step{Name: "start_database", State: "executing"})
}

// TestWorkerStorageErrors 验证授权失败直接返回，未确认检查点一律保留未知。
func TestWorkerStorageErrors(t *testing.T) {
	failure := errors.New("存储失败")
	store := rejectingStore{workerStore: &workerStore{}, authorizationError: failure}
	if err := NewWorker(store, reportingAdapter{}).execute(context.Background(), Lease{}); !errors.Is(err, failure) {
		t.Fatal("授权读取错误被吞掉", err)
	}
	for _, checkpointError := range []error{nil, failure} {
		base := &workerStore{}
		store := rejectingStore{workerStore: base, checkpointError: checkpointError}
		if err := NewWorker(store, reportingAdapter{}).execute(context.Background(), Lease{Target: Target{Desired: "running"}}); err != nil {
			t.Fatal(err)
		}
		if base.finished.State != "needs_attention" {
			t.Fatal("未确认检查点未保持未知")
		}
	}
}
