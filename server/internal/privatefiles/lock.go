// 本文件提供私有根内的跨进程文件锁，锁文件永不删除以免inode分裂。
package privatefiles

import (
	"errors"
	"os"
	"syscall"
)

// Lock 非阻塞取得空0600文件的排他锁；失败不意味着旧执行已经退出。
func (files *Root) Lock(name string) (release func() error, err error) {
	if err := files.CheckParents(name); err != nil {
		return nil, err
	}
	file, err := files.root.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, failure("LOCK_UNAVAILABLE", "无法安全取得资源锁", false, false)
	}
	defer func() {
		if err != nil {
			joinClose(&err, file)
		}
	}()
	info, err := file.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() != 0 {
		return nil, failure("LOCK_UNAVAILABLE", "资源锁不是受控0600空文件", false, false)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, lockFailure(err)
	}
	return func() error { return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close()) }, nil
}

// lockFailure 只有明确系统竞争才允许调用者有界重试，其余系统错误保持失败关闭。
func lockFailure(err error) error {
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return failure("LOCK_BUSY", "仍有外部执行器持有资源锁，不能并发操作资源", true, false)
	}
	return failure("LOCK_UNAVAILABLE", "资源锁系统调用失败，不能安全获取资源", false, false)
}
