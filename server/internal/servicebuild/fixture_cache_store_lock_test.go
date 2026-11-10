//go:build integration

// 本文件验证锁路径被替换时旧持锁者失败关闭，释放始终只作用于原描述符。
package servicebuild

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fixtureStoreAcquireOwned 由测试拥有一次释放，允许中途释放后继续验证其他句柄。
func fixtureStoreAcquireOwned(t *testing.T, cache *FixtureCache, key string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := cache.Acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	close := func() {
		once.Do(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(close)
	return close
}

// fixtureStoreReplaceLock 确定性保留旧inode并创建同名新inode，不依赖竞态时序。
func fixtureStoreReplaceLock(t *testing.T, directory, key string) (string, string) {
	t.Helper()
	path := filepath.Join(directory, key+".lock")
	detached := path + ".detached"
	if err := os.Rename(path, detached); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return path, detached
}

// TestFixtureCacheLockReplacementClosed 同名新锁可被另一句柄持有，但旧句柄绝不能继续读取或发布。
func TestFixtureCacheLockReplacementClosed(t *testing.T) {
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	first := fixtureStoreOpen(t, directory, runID)
	second := fixtureStoreOpen(t, directory, runID)
	key, err := first.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst := fixtureStoreAcquireOwned(t, first, key)
	path, detached := fixtureStoreReplaceLock(t, directory, key)
	releaseSecond := fixtureStoreAcquireOwned(t, second, key)
	if _, err := first.load(key, target, material); err == nil {
		t.Error("锁inode被替换后旧句柄仍允许读取")
	}
	if err := first.save(key, target, material, candidate, binary); err == nil {
		t.Error("锁inode被替换后旧句柄仍允许发布")
	}
	releaseFirst()
	if err := second.checkLocked(key); err != nil {
		t.Fatalf("释放原锁影响替换锁: %v", err)
	}
	fixtureStoreAssertLockBusy(t, directory, runID, key)
	for _, name := range []string{path, detached} {
		if _, err := os.Lstat(name); err != nil {
			t.Fatalf("释放擅自删除锁文件: %v", err)
		}
	}
	releaseSecond()
}

// fixtureStoreAssertLockBusy 确认原描述符释放不影响另一持锁者，竞争仍可取消。
func fixtureStoreAssertLockBusy(t *testing.T, directory, runID, key string) {
	t.Helper()
	cache := fixtureStoreOpen(t, directory, runID)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	release, err := cache.Acquire(ctx, key)
	if err == nil {
		if closeErr := release(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("应被其他句柄持锁阻止")
	}
	if ctx.Err() == nil {
		t.Fatalf("并非预期的锁竞争超时: %v", err)
	}
}

// TestFixtureCacheLockRestoredIdentity 恢复原路径后仍是原持锁inode，释放后下一句柄才能接续。
func TestFixtureCacheLockRestoredIdentity(t *testing.T) {
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	first := fixtureStoreOpen(t, directory, runID)
	key, err := first.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst := fixtureStoreAcquireOwned(t, first, key)
	path, detached := fixtureStoreReplaceLock(t, directory, key)
	if err := first.checkLocked(key); err == nil {
		t.Error("替换锁路径后仍报告原锁有效")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(detached, path); err != nil {
		t.Fatal(err)
	}
	if err := first.checkLocked(key); err != nil {
		t.Fatalf("恢复原inode后锁无法核实: %v", err)
	}
	fixtureStoreAssertLockBusy(t, directory, runID, key)
	if err := first.save(key, target, material, candidate, binary); err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	second := fixtureStoreOpen(t, directory, runID)
	fixtureStoreAcquireOwned(t, second, key)
	if proof, err := second.load(key, target, material); err != nil || proof == nil {
		t.Fatalf("原锁释放后不能接续读取: %v", err)
	}
}

// TestFixtureCacheLockAcquisitionReplacementClosed 取得锁后登记前发生替换，也必须以已打开fd识别旧inode。
func TestFixtureCacheLockAcquisitionReplacementClosed(t *testing.T) {
	target, material, _, _ := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	file, err := cache.root.OpenFile(key+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtureStoreRelease(t, func() error { return releaseFixtureLock(file) }) })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	fixtureStoreReplaceLock(t, directory, key)
	if release, err := cache.acceptLock(key, file); err == nil {
		fixtureStoreRelease(t, release)
		t.Fatal("登记时把替换后的路径快照误认为已取得的锁")
	}
	if err := cache.checkLocked(key); err == nil {
		t.Fatal("失败登记留下可用持锁状态")
	}
}
