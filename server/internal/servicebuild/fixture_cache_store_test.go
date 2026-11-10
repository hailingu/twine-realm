//go:build integration

// 本文件验证缓存内容身份和私有文件边界；结构夹具不冒充真实容器构建。
package servicebuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// fixtureStoreInput 建立纯存储测试材料，不执行数据库或容器。
func fixtureStoreInput(t *testing.T) (Target, project.SourceMaterial, *Candidate, string) {
	t.Helper()
	lease, completion := validMetadata(t)
	material := project.SourceMaterial{Commit: lease.Target.Source.PinnedCommit, Tree: lease.Target.Source.PinnedCommit, GitVersion: "git version 2.50.0", Files: []project.SourceFile{{Path: "go.mod", Size: 3, Digest: ContentDigest([]byte("mod"))}, {Path: completion.Candidate.Migrations[0].Path, Size: 3, Digest: completion.Candidate.Migrations[0].Digest}}}
	var err error
	material.Digest, err = Fingerprint(material.Files)
	if err != nil {
		t.Fatal(err)
	}
	completion.Candidate.SourceDigest = material.Digest
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return lease.Target, material, completion.Candidate, path
}

// fixtureStoreRoot 模拟runner新建单轮0700根和0600标记的契约。
func fixtureStoreRoot(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	runID := metadataID(t)
	data, err := json.Marshal(map[string]any{"schemaVersion": 1, "runId": runID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return directory, runID
}

// fixtureStoreOpen 由测试拥有句柄和关闭结果。
func fixtureStoreOpen(t *testing.T, directory, runID string) *FixtureCache {
	t.Helper()
	cache, err := OpenFixtureCache(directory, runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Error(err)
		}
	})
	return cache
}

// fixtureStoreLock 持锁覆盖读取、冷构建完成与原子发布的整个临界区。
func fixtureStoreLock(t *testing.T, cache *FixtureCache, key string) {
	t.Helper()
	release, err := cache.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	})
}

// TestFixtureCacheRoundTrip 保留原始完整证明与真实时间，跨句柄只读读取同轮内容。
func TestFixtureCacheRoundTrip(t *testing.T) {
	target, material, candidate, binary := fixtureStoreInput(t)
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	key, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStoreLock(t, cache, key)
	if value, err := cache.load(key, target, material); err != nil || value != nil {
		t.Fatalf("冷缓存应不存在: %v %v", value, err)
	}
	if err := cache.save(key, target, material, candidate, binary); err != nil {
		t.Fatal(err)
	}
	proof, err := cache.load(key, target, material)
	if err != nil {
		t.Fatal(err)
	}
	if proof == nil || !reflect.DeepEqual(proof.Origin, *candidate) || string(proof.Binary) != "binary" {
		t.Fatal("原证明或二进制未完整保留")
	}
	info, err := os.Stat(filepath.Join(directory, key, "service"))
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatalf("缓存二进制不是0400: %v", err)
	}
	if err := cache.save(key, target, material, candidate, binary); err == nil {
		t.Fatal("不得覆盖已发布条目")
	}
}

// fixtureStoreFingerprint 刷新测试来源摘要，保持每次变更都是完整有效输入。
func fixtureStoreFingerprint(t *testing.T, target *Target, material *project.SourceMaterial) {
	t.Helper()
	var err error
	material.Digest, err = Fingerprint(material.Files)
	if err != nil {
		t.Fatal(err)
	}
	target.InputDigest, err = InputFingerprint(target.Source, target.Profile, target.Environment, target.Policy, target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFixtureCacheIdentity 源码、依赖、模式、安装、策略和实现各自改变均不能命中旧键。
func TestFixtureCacheIdentity(t *testing.T) {
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	target, material, _, _ := fixtureStoreInput(t)
	original, err := cache.key(target, material)
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		change func(*Target, *project.SourceMaterial)
	}{
		{"源码字节", func(_ *Target, m *project.SourceMaterial) { m.Files[1].Digest = ContentDigest([]byte("other")) }},
		{"依赖清单", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Digest = ContentDigest([]byte("new-module")) }},
		{"文件长度", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Size++ }},
		{"执行模式", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Executable = true }},
		{"文件路径", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Path = "go.sum" }},
		{"Git工具", func(_ *Target, m *project.SourceMaterial) { m.GitVersion = "git version 2.51.0" }},
		{"工程包", func(v *Target, _ *project.SourceMaterial) { v.Profile.Digest = ContentDigest([]byte("profile")) }},
		{"工具链环境", func(v *Target, _ *project.SourceMaterial) { v.Environment.ImageID = ContentDigest([]byte("toolchain")) }},
		{"运行用户", func(v *Target, _ *project.SourceMaterial) { v.Environment.UID++ }},
		{"策略", func(v *Target, _ *project.SourceMaterial) { v.Policy.MemoryMiB++ }},
		{"安装身份", func(v *Target, _ *project.SourceMaterial) { v.InstallationID = metadataID(t) }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			current, source := target, material
			source.Files = append([]project.SourceFile(nil), material.Files...)
			test.change(&current, &source)
			fixtureStoreFingerprint(t, &current, &source)
			key, err := cache.key(current, source)
			if err != nil || key == original {
				t.Fatalf("改变身份未失效: %s %v", key, err)
			}
		})
	}
	previous := cache.implementation
	cache.implementation = ContentDigest([]byte("different-implementation"))
	changed, err := cache.key(target, material)
	cache.implementation = previous
	if err != nil || changed == original {
		t.Fatalf("实现身份未失效: %v", err)
	}
}

// TestFixtureCacheProvenance 不把随机项目、操作、commit和仓库位置当内容，原始来源仍完整保留。
func TestFixtureCacheProvenance(t *testing.T) {
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
	next := target
	next.ID, next.ProjectID, next.ServiceID = metadataID(t), metadataID(t), metadataID(t)
	next.Generation, next.CanvasRevision = "9", "13"
	next.Source.ProjectID, next.Source.ServiceID = next.ProjectID, next.ServiceID
	next.Source.RepositoryID, next.Source.RepositoryPath = metadataID(t), "/other/repository"
	next.Source.ProjectRevision, next.Source.PinnedCommit = "13", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	material.Commit, material.Tree = next.Source.PinnedCommit, next.Source.PinnedCommit
	fixtureStoreFingerprint(t, &next, &material)
	changed, err := cache.key(next, material)
	if err != nil || changed != key {
		t.Fatalf("非内容身份阻止复用: %v", err)
	}
	proof, err := cache.load(key, next, material)
	if err != nil || proof == nil || !reflect.DeepEqual(proof.Origin, *candidate) {
		t.Fatalf("丢失原始来源: %v", err)
	}
}

// TestFixtureCacheInvalidSource 未重算摘要、越界路径及重复文件不能生成可用缓存键。
func TestFixtureCacheInvalidSource(t *testing.T) {
	directory, runID := fixtureStoreRoot(t)
	cache := fixtureStoreOpen(t, directory, runID)
	tests := []struct {
		name   string
		change func(*Target, *project.SourceMaterial)
	}{
		{"旧摘要", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Digest = ContentDigest(nil) }},
		{"负长度", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Size = -1 }},
		{"过大", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Size = project.SourceExportBytes + 1 }},
		{"越界路径", func(_ *Target, m *project.SourceMaterial) { m.Files[0].Path = "../go.mod" }},
		{"重复路径", func(_ *Target, m *project.SourceMaterial) { m.Files[1].Path = m.Files[0].Path }},
		{"提交不匹配", func(_ *Target, m *project.SourceMaterial) { m.Commit = "other" }},
		{"空文件清单", func(_ *Target, m *project.SourceMaterial) { m.Files = nil }},
		{"目标摘要损坏", func(v *Target, _ *project.SourceMaterial) { v.InputDigest = ContentDigest(nil) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, material, _, _ := fixtureStoreInput(t)
			test.change(&target, &material)
			if _, err := cache.key(target, material); err == nil {
				t.Fatal("接受无效来源")
			}
		})
	}
}

// TestFixtureCacheRejectIncompleteOrigin 未完成或跨来源的候选不能发布，失败保持无命中。
func TestFixtureCacheRejectIncompleteOrigin(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Candidate)
	}{
		{"缺失检查", func(c *Candidate) { c.Checks = c.Checks[1:] }},
		{"失败检查", func(c *Candidate) { c.Checks[0].State = "failed" }},
		{"时间损坏", func(c *Candidate) { c.Checks[0].CompletedAt = "invalid" }},
		{"来源源码不符", func(c *Candidate) { c.SourceDigest = ContentDigest(nil) }},
		{"来源模板不符", func(c *Candidate) { c.Template.Digest = ContentDigest(nil) }},
		{"来源目标不符", func(c *Candidate) { c.TargetID = metadataID(t) }},
		{"二进制不符", func(c *Candidate) { c.Image.BinaryDigest = ContentDigest(nil) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, material, candidate, binary := fixtureStoreInput(t)
			directory, runID := fixtureStoreRoot(t)
			cache := fixtureStoreOpen(t, directory, runID)
			key, err := cache.key(target, material)
			if err != nil {
				t.Fatal(err)
			}
			fixtureStoreLock(t, cache, key)
			test.change(candidate)
			if err := cache.save(key, target, material, candidate, binary); err == nil {
				t.Fatal("不完整来源被发布")
			}
			if proof, err := cache.load(key, target, material); err != nil || proof != nil {
				t.Fatalf("失败发布不应产生缓存: %v", err)
			}
		})
	}
}
