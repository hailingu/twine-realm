// 本文件用跨进程项目文件锁串行外部写入；锁文件不删除，以免分裂为多个inode锁。
package mysqlruntime

import (
	"errors"
	"os"
	"syscall"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// projectLock 同一私有根下的多进程共用同一锁；非阻塞失败不意味着旧命令已结束。
func (driver *Driver) projectLock(id string) (release func() error, err error) {
	if !project.ValidID(id) {
		return nil, failure("RUNTIME_PLAN_INVALID", "项目运行身份无效", false, false)
	}
	name := "locks/" + id + ".lock"
	if err := driver.privateParents(name); err != nil {
		return nil, err
	}
	file, err := driver.root.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, failure("RUNTIME_LOCK_UNAVAILABLE", "无法安全取得项目资源锁", false, false)
	}
	defer func() {
		if err != nil {
			joinClose(&err, file)
		}
	}()
	info, err := file.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() != 0 {
		return nil, failure("RUNTIME_LOCK_UNAVAILABLE", "项目锁不是受控0600空文件", false, false)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, failure("RUNTIME_PROJECT_BUSY", "项目仍有外部执行器或锁不可用，不能并发操作资源", true, false)
	}
	return func() error { return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close()) }, nil
}
