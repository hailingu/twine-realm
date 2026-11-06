// 本文件用受控端口验证围栏、正常取消、真实失败与控制面退出，真实副作用另由Compose验收。
package servicedeploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// workerRepository 只实现本场景实际消费的方法，意外调用其他端口会使测试失败。
type workerRepository struct {
	Repository
	lease      *Lease
	authorize  func(context.Context) (bool, error)
	completed  *Completion
	yielded    bool
	checkpoint bool
}

// ClaimDeployment 只领取本次固定逻辑目标，不声称持有真实数据库租约。
func (repository *workerRepository) ClaimDeployment(context.Context, time.Time, time.Time) (*Lease, error) {
	return repository.lease, nil
}

// DeploymentAuthorized 将在途查询交给通道同步，而非用任意睡眠制造时序。
func (repository *workerRepository) DeploymentAuthorized(ctx context.Context, _ Lease) (bool, error) {
	if repository.authorize != nil {
		return repository.authorize(ctx)
	}
	return true, nil
}

// CheckpointDeployment 明确模拟当前围栏是否仍有效。
func (repository *workerRepository) CheckpointDeployment(context.Context, Lease, Step) (bool, error) {
	return repository.checkpoint, nil
}

// FinishDeployment 只记录结算意图供断言，不能作为真实SQL提交证据。
func (repository *workerRepository) FinishDeployment(_ context.Context, _ Lease, result Completion) error {
	repository.completed = &result
	return nil
}

// YieldDeployment 记录控制面退出后保留原目标的恢复入口。
func (repository *workerRepository) YieldDeployment(context.Context, Lease) error {
	repository.yielded = true
	return nil
}

// workerAdapter 不执行Docker，专门控制工作器的返回与检查点边界。
type workerAdapter struct {
	Adapter
	execute func(context.Context, func(context.Context, Step) error) (*Observation, error)
}

// Execute 只运行测试授予的逻辑动作，无法创建实际外部资源。
func (adapter workerAdapter) Execute(ctx context.Context, _ Lease, report func(context.Context, Step) error) (*Observation, error) {
	return adapter.execute(ctx, report)
}

// TestDeploymentWatchNormalStopKeepsSuccess 正常停止监测的在途查询取消不能推翻已完成结果。
func TestDeploymentWatchNormalStopKeepsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	repository := &workerRepository{authorize: func(ctx context.Context) (bool, error) { close(entered); <-ctx.Done(); return false, ctx.Err() }}
	worker := &Worker{repository: repository}
	done := make(chan error, 1)
	go func() { done <- worker.watch(ctx, cancel, Lease{}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("监测未进入受控在途查询")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("正常收尾被误报失败", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("监测协程未收回")
	}
}

// TestDeploymentWatchPreservesFailures 真正SQL失败与失效必须停止执行，不可伪装成正常取消。
func TestDeploymentWatchPreservesFailures(t *testing.T) {
	storage := errors.New("受控SQL失败")
	for _, queryErr := range []error{storage, context.Canceled, context.DeadlineExceeded, nil} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		repository := &workerRepository{authorize: func(context.Context) (bool, error) { return false, queryErr }}
		worker := &Worker{repository: repository}
		err := worker.watch(ctx, cancel, Lease{})
		if err == nil || ctx.Err() == nil {
			t.Error("真正失效没有停止执行")
		}
		if queryErr != nil && !errors.Is(err, queryErr) {
			t.Error("真正查询错误被隐藏")
		}
		cancel()
	}
}

// TestDeploymentWorkerInterruptionAndFence 控制面退出保留原操作，失效检查点不得结算成功。
func TestDeploymentWorkerInterruptionAndFence(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		repository := &workerRepository{lease: &Lease{}, checkpoint: false}
		adapter := workerAdapter{execute: func(ctx context.Context, report func(context.Context, Step) error) (*Observation, error) {
			if interrupted {
				cancel()
				return nil, ctx.Err()
			}
			return nil, report(ctx, Step{Name: "database", State: "executing"})
		}}
		worker, err := NewWorker(repository, adapter, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.runOne(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		assertWorkerDisposition(t, repository, interrupted)
	}
	if _, err := NewWorker(nil, nil, nil); err == nil {
		t.Fatal("缺省配置隐式启用工作器")
	}
}

// assertWorkerDisposition 对退出保留与真实围栏失败分别断言，不能由同一宽泛终态替代。
func assertWorkerDisposition(t *testing.T, repository *workerRepository, interrupted bool) {
	t.Helper()
	if interrupted {
		if !repository.yielded || repository.completed != nil {
			t.Fatal("控制面退出被伪报终态")
		}
	} else if repository.completed == nil || repository.completed.State != "needs_attention" || repository.completed.Code != "DEPLOY_EXECUTION_FENCED" {
		t.Fatal("旧执行越过了失效围栏")
	}
}

// TestDeploymentWorkerPanicRetainsUnknown 适配器异常不能打穿控制面，也不能留下无人回收的监测或伪成功。
func TestDeploymentWorkerPanicRetainsUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	repository := &workerRepository{lease: &Lease{}}
	adapter := workerAdapter{execute: func(context.Context, func(context.Context, Step) error) (*Observation, error) {
		panic("synthetic-private")
	}}
	worker, err := NewWorker(repository, adapter, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	escaped := false
	func() {
		defer func() {
			if recover() != nil {
				escaped = true
			}
		}()
		if err := worker.runOne(ctx); err != nil {
			t.Error("异常结果未持久结清", err)
		}
	}()
	if escaped || repository.completed == nil || repository.completed.State != "needs_attention" || repository.completed.Code != "DEPLOY_EXECUTION_PANIC" {
		t.Fatal("适配器异常逃逸或未保留未知原结果")
	}
}
