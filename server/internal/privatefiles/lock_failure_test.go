// 本文件证明资源锁只有明确竞争错误可以被重试。
package privatefiles

import (
	"errors"
	"syscall"
	"testing"
)

// TestLockFailureClassification 权限、IO和无效句柄不能伪装成锁竞争。
func TestLockFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		code  string
		retry bool
	}{{"竞争", syscall.EWOULDBLOCK, "LOCK_BUSY", true}, {"暂不可用", syscall.EAGAIN, "LOCK_BUSY", true}, {"权限", syscall.EPERM, "LOCK_UNAVAILABLE", false}, {"IO", syscall.EIO, "LOCK_UNAVAILABLE", false}, {"无效句柄", syscall.EBADF, "LOCK_UNAVAILABLE", false}} {
		t.Run(test.name, func(t *testing.T) {
			var fault *Error
			if !errors.As(lockFailure(test.cause), &fault) || fault.Code != test.code || fault.Retryable != test.retry {
				t.Fatal("资源锁错误分类错误", fault)
			}
		})
	}
}
