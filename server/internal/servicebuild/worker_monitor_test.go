// 本文件以可控在途授权查询复现监测正常收尾，不能把真实存储失败当取消隐藏。
package servicebuild

import (
	"context"
	"errors"
	"testing"
	"time"
)

// monitorAuthorization 是受控查询端口，只观察监测ctx而不执行任何Docker或SQL。
type monitorAuthorization func(context.Context, Lease) (bool, error)

// BuildAuthorized 把在途查询的完成交给测试同步，不以任意等待代替真实时序。
func (query monitorAuthorization) BuildAuthorized(ctx context.Context, lease Lease) (bool, error) {
	return query(ctx, lease)
}

// TestWatchShutdownKeepsSuccessfulBuild 正常结束发生在授权查询中时，不得推翻已完成构建。
func TestWatchShutdownKeepsSuccessfulBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	worker := &Worker{authorization: monitorAuthorization(func(ctx context.Context, _ Lease) (bool, error) {
		close(entered)
		<-ctx.Done()
		return false, ctx.Err()
	})}
	done := make(chan error, 1)
	go func() { done <- worker.watch(ctx, cancel, Lease{}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("未进入受控在途查询")
	}
	cancel()
	select {
	case err := <-done:
		_, success := validMetadata(t)
		result := buildCompletion(success.Candidate, err, false)
		if err != nil || result.State != "succeeded" || result.Candidate == nil {
			t.Fatal("正常停止监测推翻了成功构建", err, result.State)
		}
		interrupted := buildCompletion(success.Candidate, err, true)
		if interrupted.State != "needs_attention" || interrupted.Candidate != nil {
			t.Fatal("控制面退出被误报成功")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("正常停止未收回监测协程")
	}
}

// TestWatchKeepsQueryFailures 无关SQL失败或活动ctx的查询取消仍须中止执行并保留原错。
func TestWatchKeepsQueryFailures(t *testing.T) {
	storage := errors.New("受控存储不可用")
	for _, value := range []struct {
		name   string
		err    error
		cancel bool
	}{{"真实存储失败", storage, false}, {"取消同时发生的真实存储失败", storage, true}, {"内部查询取消", context.Canceled, false}, {"内部查询超时", context.DeadlineExceeded, false}} {
		t.Run(value.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			worker := &Worker{authorization: monitorAuthorization(func(context.Context, Lease) (bool, error) {
				if value.cancel {
					cancel()
				}
				return false, value.err
			})}
			err := worker.watch(ctx, cancel, Lease{})
			if !errors.Is(err, value.err) || ctx.Err() == nil {
				t.Fatal("授权查询失败被隐藏或未中止执行", err)
			}
		})
	}
}
