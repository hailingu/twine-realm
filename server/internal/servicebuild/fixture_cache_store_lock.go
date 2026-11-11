//go:build integration

// 本文件让夹具锁拥有真实描述符，路径替换不能使旧锁冒充新inode的执行屏障。
package servicebuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// Acquire 将锁文件描述符持有至释放；只重试明确竞争，取得前后均核实同一inode。
// 跨进程文件锁覆盖缓存I/O；内存mutex仅保护本句柄的描述符集合。
func (cache *FixtureCache) Acquire(ctx context.Context, key string) (release func() error, err error) {
	if err := cache.checkRun(); err != nil {
		return nil, err
	}
	if !validFixtureKey(key) {
		return nil, fmt.Errorf("夹具缓存键无效")
	}
	file, err := cache.root.OpenFile(key+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, releaseFixtureLock(file))
		}
	}()
	if err := cache.waitLock(ctx, key, file); err != nil {
		return nil, err
	}
	return cache.acceptLock(key, file)
}

// waitLock 等待期间重验原描述符和路径，替换、权限或系统错误均不伪装成锁竞争。
func (cache *FixtureCache) waitLock(ctx context.Context, key string, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := cache.matchLock(key, file); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return cache.matchLock(key, file)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// matchLock 以fstat和Lstat同时核实所有者、权限、空文件和inode，而非相信取得后的路径快照。
func (cache *FixtureCache) matchLock(key string, file *os.File) error {
	if err := cache.checkRun(); err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := cache.root.Lstat(key + ".lock")
	if err != nil {
		return err
	}
	if !fixturePrivateInfo(opened, 0600, false) || !fixturePrivateInfo(current, 0600, false) || opened.Size() != 0 || current.Size() != 0 || !os.SameFile(opened, current) {
		return fmt.Errorf("夹具缓存锁描述符或路径身份已改变")
	}
	return nil
}

// acceptLock 记录持锁描述符；重复释放只作用于原fd且从不删除或接管替换文件。
func (cache *FixtureCache) acceptLock(key string, file *os.File) (func() error, error) {
	if err := cache.matchLock(key, file); err != nil {
		return nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.held[key] != nil {
		return nil, fmt.Errorf("本缓存句柄已持有该内容锁")
	}
	cache.held[key] = file
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			cache.mu.Lock()
			if cache.held[key] == file {
				delete(cache.held, key)
			}
			cache.mu.Unlock()
			releaseErr = releaseFixtureLock(file)
		})
		return releaseErr
	}, nil
}

// releaseFixtureLock 只解锁和关闭取得时的描述符，不触碰任何当前路径对象。
func releaseFixtureLock(file *os.File) error {
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}

// checkLocked 每次读取和发布重新核实同一锁inode，替换后的旧持锁者必须失败关闭。
func (cache *FixtureCache) checkLocked(key string) error {
	if !validFixtureKey(key) {
		return fmt.Errorf("夹具缓存键无效")
	}
	cache.mu.Lock()
	file := cache.held[key]
	cache.mu.Unlock()
	if file == nil {
		return fmt.Errorf("夹具缓存操作未持有内容锁")
	}
	return cache.matchLock(key, file)
}
