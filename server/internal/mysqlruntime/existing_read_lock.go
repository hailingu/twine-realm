// 本文件只为只读冻结提供有界锁竞争等待，不重试任何外部动作。
package mysqlruntime

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
	"time"
)

// WithExistingProjectRead 至多等待原项目锁两秒，锁内完整只读核实只执行一次。
func (driver *Driver) WithExistingProjectRead(ctx context.Context, id string, action func() error) (err error) {
	release, err := driver.waitExistingReadLock(ctx, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return action()
}

// waitExistingReadLock 只重试明确的flock竞争，非竞争错误与调用者取消立即返回。
func (driver *Driver) waitExistingReadLock(ctx context.Context, id string) (func() error, error) {
	if !project.ValidID(id) {
		return nil, failure("RUNTIME_PLAN_INVALID", "项目运行身份无效", false, false)
	}
	wait, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var busy error = failure("RUNTIME_PROJECT_BUSY", "只读冻结等待原锁超过期限", true, false)
	for {
		if err := existingReadWaitError(ctx, wait, busy); err != nil {
			return nil, err
		}
		release, err := driver.files.Lock("locks/" + id + ".lock")
		if err == nil {
			return release, nil
		}
		var fault *privatefiles.Error
		if !errors.As(err, &fault) || fault.Code != "LOCK_BUSY" {
			return nil, privateFileFailure(err)
		}
		busy = err
		if err := waitExistingReadTick(ctx, wait, ticker.C, busy); err != nil {
			return nil, err
		}
	}
}

// waitExistingReadTick 等待下次只读锁尝试，保留调用者取消与实际竞争原因。
func waitExistingReadTick(ctx, wait context.Context, tick <-chan time.Time, busy error) error {
	if err := existingReadWaitError(ctx, wait, busy); err != nil {
		return err
	}
	select {
	case <-wait.Done():
	case <-tick:
	}
	return existingReadWaitError(ctx, wait, busy)
}

// existingReadWaitError 每次锁尝试前显式核对期限，父调用者取消优先于内部超限。
func existingReadWaitError(ctx, wait context.Context, busy error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if wait.Err() != nil {
		return privateFileFailure(busy)
	}
	return nil
}
