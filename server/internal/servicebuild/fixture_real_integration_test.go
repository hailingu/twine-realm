//go:build integration

// 本文件通过真实Git、隔离沙箱和Buildx验证缓存冷构建、命中身份隔离及取消收回。
package servicebuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRealFixtureBuildCache 同轮相同字节只复用三个检查证明，每次仍产生独立归属的真实镜像。
func TestRealFixtureBuildCache(t *testing.T) {
	f, directory, runID, owned := realCacheFixture(t)
	cache := fixtureStoreOpen(t, directory, runID)
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	report := func(_ context.Context, step Step) (bool, error) {
		t.Log("真实缓存检查点", step.Name, step.State)
		return true, nil
	}
	cold, hit, err := cache.Execute(ctx, f.driver, f.target, f.operationID, f.inspector, report)
	if err != nil || hit || cold == nil {
		t.Fatal("空缓存没有真实构建", err)
	}
	t.Cleanup(func() { cleanupBuildImage(t, f.driver, f.driver.newSandbox(f.target, f.operationID), cold.Image) })
	other := fixtureOtherTarget(t, f)
	*owned = append(*owned, other)
	// 同一固定commit的脏工作区仍不能污染命中的原始字节。
	if err := os.WriteFile(filepath.Join(f.repository, "cmd/service/main.go"), []byte("dirty fixture must not build"), 0600); err != nil {
		t.Fatal(err)
	}
	before := gitBuild(t, f.repository, "status", "--porcelain=v1")
	warm, hit, err := cache.Execute(ctx, other.driver, other.target, other.operationID, other.inspector, report)
	if err != nil || !hit || warm == nil {
		t.Fatal("同轮固定内容未命中", err)
	}
	t.Cleanup(func() {
		cleanupBuildImage(t, other.driver, other.driver.newSandbox(other.target, other.operationID), warm.Image)
	})
	if warm.OperationID == cold.OperationID || warm.TargetID == cold.TargetID || warm.Image.ID == cold.Image.ID || warm.Image.Tag == cold.Image.Tag || warm.SourceDigest != cold.SourceDigest || warm.Image.BinaryDigest != cold.Image.BinaryDigest {
		t.Fatal("缓存共享了可变归属或改变固定二进制")
	}
	for _, value := range []*Candidate{cold, warm} {
		if err := f.driver.VerifyCandidate(ctx, *value); err != nil {
			t.Fatal("真实候选归属核实失败", err)
		}
	}
	if !reflect.DeepEqual(fixtureHeavyChecks(cold), fixtureHeavyChecks(warm)) || gitBuild(t, f.repository, "status", "--porcelain=v1") != before {
		t.Fatal("命中改变原检查时间、摘要或工作区")
	}
	cancelled := fixtureOtherTarget(t, f)
	*owned = append(*owned, cancelled)
	assertFixtureCancellation(t, cache, cancelled)
}

// realCacheFixture 仅在本次所有沙箱、卷及镜像都确认收回后删除根，未知保留安装与缓存证明。
func realCacheFixture(t *testing.T) (realBuildFixture, string, string, *[]realBuildFixture) {
	t.Helper()
	root, err := os.MkdirTemp("", "twine-real-fixture-cache-")
	if err != nil {
		t.Fatal(err)
	}
	f := realBuildFixtureAt(t, root)
	owned := []realBuildFixture{f}
	t.Cleanup(func() { cleanupRealFixtureFiles(t, f.driver, root, owned) })
	directory, runID := filepath.Join(root, "cache"), buildID(t)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(fixtureRunMarker{fixtureCacheVersion, runID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return f, directory, runID, &owned
}

// cleanupRealFixtureFiles 不接管未确认资源；只读复核失败时保留明确本轮根供恢复。
func cleanupRealFixtureFiles(t *testing.T, driver *Driver, root string, owned []realBuildFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, f := range owned {
		box := driver.newSandbox(f.target, f.operationID)
		container, containerErr := driver.inspectSandbox(ctx, box)
		volume, volumeErr := driver.inspectOutputVolume(ctx, box)
		if containerErr != nil || volumeErr != nil || container != nil || volume != nil {
			t.Error("缓存真实验收资源未知，保留安装材料", root)
			return
		}
	}
	data, err := driver.run(ctx, "image", "ls", "--filter", "label=io.twine-realm.build.installation="+driver.InstallationID(), "--format", "{{json .}}")
	if err != nil || strings.TrimSpace(string(data)) != "" {
		t.Error("缓存真实验收镜像未确认收回，保留安装材料", root)
		return
	}
	if err := os.RemoveAll(root); err != nil {
		t.Error("缓存真实验收材料未清理", err)
	}
}

// fixtureOtherTarget 创建完全独立目标身份，固定源码内容保持不变。
func fixtureOtherTarget(t *testing.T, original realBuildFixture) realBuildFixture {
	t.Helper()
	value := original
	value.target.ID, value.target.ProjectID, value.target.ServiceID = buildID(t), buildID(t), buildID(t)
	value.target.Source.ProjectID, value.target.Source.ServiceID, value.target.Source.RepositoryID = value.target.ProjectID, value.target.ServiceID, buildID(t)
	value.operationID = buildID(t)
	var err error
	value.target.InputDigest, err = InputFingerprint(value.target.Source, value.target.Profile, value.target.Environment, value.target.Policy, value.target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// fixtureHeavyChecks 返回真实重阶段原证据，比较包括完成时间且不归一化。
func fixtureHeavyChecks(candidate *Candidate) []Check {
	values := []Check{}
	for _, check := range candidate.Checks {
		if check.Name == "vet" || check.Name == "test" || check.Name == "build" {
			values = append(values, check)
		}
	}
	return values
}

// assertFixtureCancellation 命中后仍可在真实模块检查边界取消，必须收回原沙箱和输出卷且无候选。
func assertFixtureCancellation(t *testing.T, cache *FixtureCache, f realBuildFixture) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	candidate, hit, err := cache.Execute(ctx, f.driver, f.target, f.operationID, f.inspector, func(_ context.Context, step Step) (bool, error) {
		if step.Name == "module" && step.State == "succeeded" {
			cancel()
		}
		return true, nil
	})
	if err == nil || !hit || candidate != nil {
		t.Fatal("缓存命中取消后仍生成候选")
	}
	box := f.driver.newSandbox(f.target, f.operationID)
	container, containerErr := f.driver.inspectSandbox(context.Background(), box)
	volume, volumeErr := f.driver.inspectOutputVolume(context.Background(), box)
	if containerErr != nil || volumeErr != nil || container != nil || volume != nil {
		t.Fatal("缓存取消临时资源没有完整收回", containerErr, volumeErr)
	}
}
