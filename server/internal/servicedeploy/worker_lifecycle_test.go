// 本文件验证工作器生命周期的错误归属，受控端口不作为SQL或Docker成功证据。
package servicedeploy

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// lifecycleRepository 让生命周期测试只实现自己明确授予的领取、查询与记录行为。
type lifecycleRepository struct {
	Repository
	claim   func(context.Context) (*Lease, error)
	targets func(context.Context) ([]Target, error)
	record  func(context.Context, Target, *Observation, string, time.Time) error
}

// ClaimDeployment 将领取异常限定在当前工作器，不模拟真实租约。
func (repository lifecycleRepository) ClaimDeployment(ctx context.Context, _, _ time.Time) (*Lease, error) {
	return repository.claim(ctx)
}

// DeploymentTargets 只读返回受控目标或读取失败。
func (repository lifecycleRepository) DeploymentTargets(ctx context.Context) ([]Target, error) {
	return repository.targets(ctx)
}

// ObserveDeployment 保留真实采集码、时间与记录失败供调用者断言。
func (repository lifecycleRepository) ObserveDeployment(ctx context.Context, target Target, fact *Observation, code string, now time.Time) error {
	return repository.record(ctx, target, fact, code, now)
}

// observingAdapter 不拥有外部资源，仅控制采集结果和失败时序。
type observingAdapter struct {
	Adapter
	inspect func(context.Context, Target) (*Observation, error)
}

// Inspect 只完成受控一次采集，取消仍由传入context拥有。
func (adapter observingAdapter) Inspect(ctx context.Context, target Target) (*Observation, error) {
	return adapter.inspect(ctx, target)
}

// TestDeploymentRunStorageFailuresRemainSafe 重复存储故障可停止；固定告警不能泄露底层原文。
func TestDeploymentRunStorageFailuresRemainSafe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	private := errors.New("synthetic-private-storage")
	claims := 0
	repository := lifecycleRepository{
		claim: func(context.Context) (*Lease, error) {
			claims++
			if claims == 2 {
				cancel()
			}
			return nil, private
		},
		targets: func(context.Context) ([]Target, error) { return nil, private },
	}
	var logs bytes.Buffer
	worker := Worker{repository: repository, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	if err := worker.Run(ctx); err != nil || claims != 2 {
		t.Fatal("存储故障后的停止未收尾", err)
	}
	output := logs.String()
	if strings.Contains(output, private.Error()) || !strings.Contains(output, "DEPLOY_STORAGE_UNAVAILABLE") || !strings.Contains(output, "DEPLOY_OBSERVATION_UNAVAILABLE") {
		t.Fatal("故障未显式安全分类")
	}
}

// TestDeploymentLifecyclePanicsStayUnknown 领取与监测异常都以安全未知退出，监测必须取消原执行。
func TestDeploymentLifecyclePanicsStayUnknown(t *testing.T) {
	worker := Worker{repository: lifecycleRepository{claim: func(context.Context) (*Lease, error) { panic("synthetic-private-claim") }}}
	err := worker.Run(context.Background())
	assertLifecycleFailure(t, err, "DEPLOY_WORKER_PANIC")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	worker.repository = &workerRepository{authorize: func(context.Context) (bool, error) { panic("synthetic-private-monitor") }}
	err = worker.watch(ctx, cancel, Lease{})
	assertLifecycleFailure(t, err, "DEPLOY_MONITOR_PANIC")
	if ctx.Err() == nil {
		t.Fatal("许可监测异常后原执行仍可继续")
	}
}

// assertLifecycleFailure 不接受异常原文泄露或正常成功结算。
func assertLifecycleFailure(t *testing.T, err error, expected string) {
	t.Helper()
	code, message, unknown := Diagnostic(err)
	if err == nil || code != expected || !unknown || strings.Contains(message, "synthetic-private") {
		t.Fatal("生命周期异常未保留安全未知结果", code)
	}
}

// TestDeploymentObservationFailureIsRecordedThenReturned 采集失败仍须保存未知分类，记录失败不能伪报成功。
func TestDeploymentObservationFailureIsRecordedThenReturned(t *testing.T) {
	storage := errors.New("受控记录失败")
	recorded := false
	target := Target{ID: "original"}
	repository := lifecycleRepository{
		targets: func(context.Context) ([]Target, error) { return []Target{target}, nil },
		record: func(ctx context.Context, got Target, fact *Observation, code string, now time.Time) error {
			recorded = true
			if ctx.Err() != nil || got.ID != target.ID || fact != nil || code != "DEPLOY_RESULT_UNKNOWN" || now.IsZero() {
				t.Error("未保存原目标的真实失败分类和时间")
			}
			return storage
		},
	}
	adapter := observingAdapter{inspect: func(context.Context, Target) (*Observation, error) { return nil, errors.New("synthetic-private-probe") }}
	worker := Worker{repository: repository, adapter: adapter}
	if err := worker.observe(context.Background()); !errors.Is(err, storage) || !recorded {
		t.Fatal("持久观察失败被隐藏", err)
	}
}

// TestDeploymentObservationCanceledBeforeInspect 目标查询后已取消不再访问执行器或保存新的观察。
func TestDeploymentObservationCanceledBeforeInspect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository := lifecycleRepository{targets: func(context.Context) ([]Target, error) {
		cancel()
		return []Target{{ID: "original"}}, nil
	}}
	worker := Worker{repository: repository}
	if err := worker.observe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("取消后仍继续采集", err)
	}
}
