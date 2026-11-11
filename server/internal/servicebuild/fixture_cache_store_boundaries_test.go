//go:build integration

// 本文件以真实文件系统与子进程验证损坏关闭和跨进程发布锁，不启动外部服务。
package servicebuild

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureStoreWrite 只修改测试自有文件并保留精确的私有权限。
func fixtureStoreWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, 0600); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// TestFixtureCacheRejectUnsafeRoot 不初始化、不修复、拒绝跨轮或不安全标记。
func TestFixtureCacheRejectUnsafeRoot(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *string)
	}{
		{"跨轮", func(t *testing.T, _ string, id *string) { *id = metadataID(t) }},
		{"宽权限根", func(t *testing.T, p string, _ *string) {
			if err := os.Chmod(p, 0755); err != nil {
				t.Fatal(err)
			}
		}},
		{"缺标记", func(t *testing.T, p string, _ *string) {
			if err := os.Remove(filepath.Join(p, "run.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"宽权限标记", func(t *testing.T, p string, _ *string) {
			if err := os.Chmod(filepath.Join(p, "run.json"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"额外字段", func(t *testing.T, p string, _ *string) {
			fixtureStoreWrite(t, filepath.Join(p, "run.json"), []byte(`{"schemaVersion":1,"extra":true}`), 0600)
		}},
		{"畸形标记", func(t *testing.T, p string, _ *string) {
			fixtureStoreWrite(t, filepath.Join(p, "run.json"), []byte(`{"schemaVersion":`), 0600)
		}},
		{"标记硬链接", func(t *testing.T, p string, _ *string) {
			if err := os.Link(filepath.Join(p, "run.json"), filepath.Join(t.TempDir(), "linked")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory, runID := fixtureStoreRoot(t)
			test.change(t, directory, &runID)
			fixtureStoreAssertOpenRejected(t, directory, runID)
		})
	}
}

// fixtureStoreAssertOpenRejected 统一核实拒绝结果及意外返回句柄的清理责任。
func fixtureStoreAssertOpenRejected(t *testing.T, directory, runID string) {
	t.Helper()
	cache, err := OpenFixtureCache(directory, runID)
	if cache != nil {
		fixtureStoreRelease(t, cache.Close)
	}
	if err == nil {
		t.Fatal("不安全缓存根被接受")
	}
}

// fixtureStoreRelease 保留关闭错误，供测试中途释放与清理回调共用。
func fixtureStoreRelease(t *testing.T, release func() error) {
	t.Helper()
	if err := release(); err != nil {
		t.Error(err)
	}
}

// fixtureStoreCorruption 描述真实文件系统的单项损坏，不模拟存储返回值。
type fixtureStoreCorruption struct {
	name   string
	change func(*testing.T, string)
}

// fixtureStoreContentCorruptions 聚合长度、权限和清单完整性破坏。
func fixtureStoreContentCorruptions() []fixtureStoreCorruption {
	return []fixtureStoreCorruption{{"额外文件", func(t *testing.T, p string) { fixtureStoreWrite(t, filepath.Join(p, "extra"), []byte("unknown"), 0600) }},
		{"二进制字节改变", func(t *testing.T, p string) {
			fixtureStoreWrite(t, filepath.Join(p, "service"), []byte("changed"), 0400)
		}},
		{"二进制可写", func(t *testing.T, p string) {
			if err := os.Chmod(filepath.Join(p, "service"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"二进制为空", func(t *testing.T, p string) { fixtureStoreWrite(t, filepath.Join(p, "service"), nil, 0400) }},
		{"缺少二进制", func(t *testing.T, p string) {
			if err := os.Remove(filepath.Join(p, "service")); err != nil {
				t.Fatal(err)
			}
		}},
		{"缺少清单", func(t *testing.T, p string) {
			if err := os.Remove(filepath.Join(p, "manifest.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"清单畸形", func(t *testing.T, p string) {
			fixtureStoreWrite(t, filepath.Join(p, "manifest.json"), []byte("{"), 0600)
		}},
		{"清单宽权限", func(t *testing.T, p string) {
			if err := os.Chmod(filepath.Join(p, "manifest.json"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"清单超限", func(t *testing.T, p string) {
			fixtureStoreWrite(t, filepath.Join(p, "manifest.json"), []byte(strings.Repeat("x", fixtureManifestBytes+1)), 0600)
		}},
		{"目录宽权限", func(t *testing.T, p string) {
			if err := os.Chmod(p, 0755); err != nil {
				t.Fatal(err)
			}
		}},
	}
}

// fixtureStoreLinkCorruptions 聚合链接和特殊对象破坏，必须非阻塞拒绝。
func fixtureStoreLinkCorruptions() []fixtureStoreCorruption {
	return []fixtureStoreCorruption{
		{"二进制硬链接", func(t *testing.T, p string) {
			if err := os.Link(filepath.Join(p, "service"), filepath.Join(t.TempDir(), "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"二进制符号链接", func(t *testing.T, p string) {
			name := filepath.Join(p, "service")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(p, "manifest.json"), name); err != nil {
				t.Fatal(err)
			}
		}},
		{"二进制管道", func(t *testing.T, p string) {
			name := filepath.Join(p, "service")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(name, 0400); err != nil {
				t.Fatal(err)
			}
		}},
	}
}

// TestFixtureCacheCorruptionClosed 任一文件损坏、链接、特殊类型或权限改变都不是安全miss。
func TestFixtureCacheCorruptionClosed(t *testing.T) {
	tests := append(fixtureStoreContentCorruptions(), fixtureStoreLinkCorruptions()...)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixtureStoreAssertCorruptionClosed(t, test)
		})
	}
}

// fixtureStoreAssertCorruptionClosed 为每项损坏独立创建并核实真实条目。
func fixtureStoreAssertCorruptionClosed(t *testing.T, test fixtureStoreCorruption) {
	t.Helper()
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStoreLock(t, cache, key)
	if err := cache.save(key, target, material, candidate, binary); err != nil {
		t.Fatal(err)
	}
	test.change(t, filepath.Join(directory, key))
	if proof, err := cache.load(key, target, material); err == nil || proof != nil {
		t.Fatalf("损坏未关闭: %v", err)
	}
}

// TestFixtureCachePendingAndLock 未完成发布不得被误作miss，无锁调用及错误键不能操作缓存。
func TestFixtureCachePendingAndLock(t *testing.T) {
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load(key, target, material); err == nil {
		t.Fatal("允许无锁读取")
	}
	if err := cache.save(key, target, material, candidate, binary); err == nil {
		t.Fatal("允许无锁发布")
	}
	if _, err := cache.Acquire(context.Background(), "../outside"); err == nil {
		t.Fatal("允许路径键")
	}
	fixtureStoreLock(t, cache, key)
	if err := os.Mkdir(filepath.Join(directory, key+".pending"), 0700); err != nil {
		t.Fatal(err)
	}
	if proof, err := cache.load(key, target, material); err == nil || proof != nil {
		t.Fatal("未完成发布被当作miss")
	}
	if err := cache.save(key, target, material, candidate, binary); err == nil {
		t.Fatal("覆盖未完成发布")
	}
}

// TestFixtureCacheManifestTampering 清单中的轮次、实现、源码和检查证据均重新校验。
func TestFixtureCacheManifestTampering(t *testing.T) {
	changes := []struct {
		name   string
		change func(*fixtureManifest)
	}{
		{"轮次", func(m *fixtureManifest) { m.RunID = metadataID(t) }},
		{"版本", func(m *fixtureManifest) { m.SchemaVersion++ }},
		{"内容键", func(m *fixtureManifest) { m.Key = strings.Repeat("a", 64) }},
		{"实现", func(m *fixtureManifest) { m.Implementation = ContentDigest(nil) }},
		{"源码", func(m *fixtureManifest) { m.Material.Files[0].Digest = ContentDigest(nil) }},
		{"缺检查", func(m *fixtureManifest) { m.Origin.Checks = m.Origin.Checks[1:] }},
		{"输入", func(m *fixtureManifest) { m.Target.InputDigest = ContentDigest(nil) }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			fixtureStoreAssertManifestTampering(t, test.change)
		})
	}
}

// fixtureStoreAssertManifestTampering 将单项清单破坏与真实加载结果完整绑定。
func fixtureStoreAssertManifestTampering(t *testing.T, change func(*fixtureManifest)) {
	t.Helper()
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStoreLock(t, cache, key)
	if err := cache.save(key, target, material, candidate, binary); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, key, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	change(&manifest)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStoreWrite(t, path, data, 0600)
	if _, err := cache.load(key, target, material); err == nil {
		t.Fatal("篡改清单被接受")
	}
}

// TestFixtureCacheProcessHelper 仅由父测试显式启动，证明真实进程间锁与同程序内容复用。
func TestFixtureCacheProcessHelper(t *testing.T) {
	mode := os.Getenv("TWINE_FIXTURE_STORE_HELPER")
	if mode == "" {
		return
	}
	directory, runID, key := os.Getenv("TWINE_FIXTURE_STORE_ROOT"), os.Getenv("TWINE_FIXTURE_STORE_RUN"), os.Getenv("TWINE_FIXTURE_STORE_KEY")
	cache := fixtureStoreOpen(t, directory, runID)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	release, err := cache.Acquire(ctx, key)
	if mode == "blocked" {
		fixtureStoreAssertBlocked(t, ctx, release, err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureStoreRelease(t, release)
	data, err := os.ReadFile(filepath.Join(directory, key, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	proof, err := cache.load(key, manifest.Target, manifest.Material)
	if err != nil || proof == nil || string(proof.Binary) != "binary" {
		t.Fatalf("跨进程读取失败: %v", err)
	}
}

// fixtureStoreAssertBlocked 区分明确可取消锁竞争和其他失败，不吞掉意外成功的释放错误。
func fixtureStoreAssertBlocked(t *testing.T, ctx context.Context, release func() error, err error) {
	t.Helper()
	if err == nil {
		fixtureStoreRelease(t, release)
		t.Fatal("跨进程锁未阻止读取")
	}
	if ctx.Err() == nil {
		t.Fatalf("不是可取消锁竞争: %v", err)
	}
}

// TestFixtureCacheCrossProcess 相同二进制不同进程先被锁阻止，释放后读取同一已发布完整条目。
func TestFixtureCacheCrossProcess(t *testing.T) {
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	release := fixtureStoreAcquireOwned(t, cache, key)
	if err := cache.save(key, target, material, candidate, binary); err != nil {
		t.Fatal(err)
	}
	fixtureStoreRunProcess(t, "blocked", directory, runID, key)
	release()
	fixtureStoreRunProcess(t, "read", directory, runID, key)
}

// fixtureStoreRunProcess 在有界时间内执行当前测试程序，不调用工具链、数据库或Docker。
func fixtureStoreRunProcess(t *testing.T, mode, directory, runID, key string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, "-test.run=^TestFixtureCacheProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "TWINE_FIXTURE_STORE_HELPER="+mode, "TWINE_FIXTURE_STORE_ROOT="+directory, "TWINE_FIXTURE_STORE_RUN="+runID, "TWINE_FIXTURE_STORE_KEY="+key)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("子进程验证失败: %v %s", err, output)
	}
}
