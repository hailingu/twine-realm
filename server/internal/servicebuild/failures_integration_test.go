//go:build integration

// 本文件验证真实错误和取消不能生成镜像候选，并核实所有临时资源已收回。
package servicebuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// commitBuildFixture 只更新测试仓库输入，不修改产品已受理目标。
func commitBuildFixture(t *testing.T, f *realBuildFixture) {
	t.Helper()
	gitBuild(t, f.repository, "add", ".")
	gitBuild(t, f.repository, "-c", "user.name=隔离构建测试", "-c", "user.email=build@example.invalid", "commit", "-m", "构造受控失败")
	f.target.Source.PinnedCommit = gitBuild(t, f.repository, "rev-parse", "HEAD")
	var err error
	f.target.InputDigest, err = InputFingerprint(f.target.Source, f.target.Profile, f.target.Environment, f.target.Policy, f.target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRealInvalidMaterialsAndCancellation 规范漂移、格式错误与取消均保持无候选，原工作区保持不变。
func TestRealInvalidMaterialsAndCancellation(t *testing.T) {
	for _, phase := range []string{"documents", "format", "cancel"} {
		t.Run(phase, func(t *testing.T) { realBuildFailure(t, phase) })
	}
}

// realBuildFailure 使用真实模板、固定commit、Docker和临时输出卷，而非模拟检查结果。
func realBuildFailure(t *testing.T, phase string) {
	t.Helper()
	f := newRealBuildFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepareFailureMaterial(t, &f, phase)
	before := gitBuild(t, f.repository, "status", "--porcelain=v1")
	candidate, err := f.driver.Execute(ctx, f.target, f.operationID, f.inspector, func(_ context.Context, step Step) (bool, error) {
		if phase == "cancel" && step.Name == "vet" && step.State == "executing" {
			cancel()
		}
		return true, nil
	})
	if err == nil || candidate != nil {
		t.Fatal("失败或取消发布了候选")
	}
	code, _, unknown := diagnostic(err)
	expected := map[string]string{"documents": "ENGINEERING_DOCUMENT_CHANGED", "format": "BUILD_FORMAT_FAILED", "cancel": "BUILD_EXECUTION_STOPPED"}[phase]
	if code != expected || unknown {
		t.Fatalf("受控失败分类不符：%s / %v / %v", code, unknown, err)
	}
	if gitBuild(t, f.repository, "status", "--porcelain=v1") != before {
		t.Fatal("失败构建修改了原工作区")
	}
	box := f.driver.newSandbox(f.target, f.operationID)
	container, containerErr := f.driver.inspectSandbox(context.Background(), box)
	volume, volumeErr := f.driver.inspectOutputVolume(context.Background(), box)
	if err := errors.Join(containerErr, volumeErr); err != nil || container != nil || volume != nil {
		t.Fatal("失败构建临时资源未核实收回：", err)
	}
}

// prepareFailureMaterial 只构造本测试输入，提交后再由产品固定对象导出。
func prepareFailureMaterial(t *testing.T, f *realBuildFixture, phase string) {
	t.Helper()
	if phase == "documents" {
		if err := os.WriteFile(filepath.Join(f.repository, "docs/engineering/general.md"), []byte("被改变的规范"), 0600); err != nil {
			t.Fatal(err)
		}
		commitBuildFixture(t, f)
	}
	if phase == "format" {
		if err := os.WriteFile(filepath.Join(f.repository, "unformatted.go"), []byte("package main\nfunc example(){println(1)}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		commitBuildFixture(t, f)
	}
}
