// 本文件用真实flock证明只读冻结的释放、截止、取消及非竞争错误边界。
package mysqlruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExistingReadLockReleased 只读冻结等待原拥有者释放，之后只执行一次。
func TestExistingReadLockReleased(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	id := testResource(t, driver).ProjectID
	release, err := driver.projectLock(id)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() { time.Sleep(80 * time.Millisecond); released <- release() }()
	calls := 0
	err = driver.WithExistingProjectRead(context.Background(), id, func() error { calls++; return nil })
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil || calls != 1 {
		t.Fatal("未等待原锁释放后执行一次", err, calls)
	}
}

// TestExistingReadLockDeadlineAndCancel 竞争等待尊重调用者截止与取消，不执行核实。
func TestExistingReadLockDeadlineAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "截止", true: "取消"}[cancelled], func(t *testing.T) {
			assertExistingReadCancelled(t, cancelled)
		})
	}
}

// TestExistingReadLockUnavailable 损坏锁文件不能作为竞争重试或执行动作。
func TestExistingReadLockUnavailable(t *testing.T) {
	driver, root := openPrivateFixture(t)
	id := testResource(t, driver).ProjectID
	if err := os.WriteFile(filepath.Join(root, "locks", id+".lock"), []byte("not-empty"), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := driver.WithExistingProjectRead(context.Background(), id, func() error { t.Fatal("损坏锁仍执行"); return nil })
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "RUNTIME_LOCK_UNAVAILABLE" {
		t.Fatal("非竞争错误未保留", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("非竞争错误被等待")
	}
}

// TestExistingReadLockBound 不受调用者无限上下文影响，真实持锁超过上限仍不调用核实。
func TestExistingReadLockBound(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	id := testResource(t, driver).ProjectID
	release, err := driver.projectLock(id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	start := time.Now()
	err = driver.WithExistingProjectRead(context.Background(), id, func() error { t.Fatal("超过等待上限仍执行"); return nil })
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "RUNTIME_PROJECT_BUSY" {
		t.Fatal("等待上限错误丢失", err)
	}
	if elapsed := time.Since(start); elapsed < 1900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("未保持两秒有界等待", elapsed)
	}
}

// TestExistingReadExpiredTick 已过期且tick可读时不能随机放行下一次锁尝试。
func TestExistingReadExpiredTick(t *testing.T) {
	wait, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	tick := make(chan time.Time, 1)
	tick <- time.Now()
	busy := failure("RUNTIME_PROJECT_BUSY", "真实锁竞争", true, false)
	for range 100 {
		err := waitExistingReadTick(context.Background(), wait, tick, busy)
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "RUNTIME_PROJECT_BUSY" {
			t.Fatal("已过期tick放行锁尝试", err)
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := waitExistingReadTick(ctx, wait, tick, busy); !errors.Is(err, context.Canceled) {
		t.Fatal("父取消未优先", err)
	}
}

// assertExistingReadCancelled 分别持有真实锁并核实截止或取消，不执行只读回调。
func assertExistingReadCancelled(t *testing.T, cancelled bool) {
	t.Helper()
	driver, _ := openPrivateFixture(t)
	id := testResource(t, driver).ProjectID
	release, err := driver.projectLock(id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if cancelled {
		time.AfterFunc(30*time.Millisecond, cancel)
	}
	err = driver.WithExistingProjectRead(ctx, id, func() error { t.Fatal("截止或取消后执行"); return nil })
	expected := context.DeadlineExceeded
	if cancelled {
		expected = context.Canceled
	}
	if !errors.Is(err, expected) {
		t.Fatal("未保留调用者取消原因", err)
	}
}
