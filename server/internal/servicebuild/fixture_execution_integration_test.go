//go:build integration

// 本文件核实固定夹具命中仍穿过真实前置检查，复用证明不能伪装为新执行。
package servicebuild

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// TestFixtureCachePreservesChecks 命中只复用三个重阶段，原时间与摘要保留，其他阶段仍经原检查。
func TestFixtureCachePreservesChecks(t *testing.T) {
	boundary := newRecoveryBoundary(t)
	boundary.driver.execute = checkFailureCommand(boundary, "never-match")
	steps := []Step{}
	work := execution{driver: boundary.driver, box: boundary.box, report: func(_ context.Context, step Step) (bool, error) {
		steps = append(steps, step)
		return true, nil
	}}
	material := project.SourceMaterial{Files: []project.SourceFile{{Path: "cmd/service/main.go"}}}
	checks := []Check{}
	for _, name := range []string{"vet", "test", "build"} {
		checks = append(checks, Check{Name: name, State: "succeeded", ToolVersion: "go1.27.3", OutputDigest: ContentDigest([]byte(name)), CompletedAt: "2026-11-01T00:00:00Z"})
	}
	if err := work.checkFixtureMaterial(context.Background(), boundary.container.ID, material, checks); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(work.checks[len(work.checks)-3:], checks) {
		t.Fatal("夹具复用重写原检查证据")
	}
	for _, step := range steps {
		if step.Name == "vet" || step.Name == "test" || step.Name == "build" {
			t.Fatal("复用证明冒充当前重新执行的检查点")
		}
	}
	if len(steps) != 10 || len(boundary.container.NetworkSettings.Networks) != 0 {
		t.Fatal("命中跳过前置检查或实际断网核实")
	}
}

// TestFixtureCachePublicationFailureCleanup 发布失败后只清理已核实的当前tag，错误归属及删除未知均保留失败。
func TestFixtureCachePublicationFailureCleanup(t *testing.T) {
	for _, mode := range []string{"valid", "labels", "remove-failed"} {
		t.Run(mode, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			digest := ContentDigest([]byte("current-image"))
			image := Image{ID: digest, IDKind: "config", ExportDigest: digest, ConfigDigest: digest, Tag: "test-candidate:local"}
			removed := 0
			boundary.driver.execute = fixtureDiscardCommand(t, boundary, digest, mode, &removed)
			err := boundary.driver.discardFixtureCandidate(boundary.box.target, &Candidate{OperationID: boundary.box.operationID, Image: image})
			assertFixtureDiscardOutcome(t, mode, removed, err)
		})
	}
}

// fixtureDiscardCommand 仅模拟已加载镜像与删除结果，不允许测试替身实际访问Docker。
func fixtureDiscardCommand(t *testing.T, boundary *recoveryBoundary, digest, mode string, removed *int) dockerCommand {
	t.Helper()
	return func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "image inspect") {
			return loadedImageJSON(t, boundary, digest, mode), nil
		}
		if strings.Contains(command, "image rm") {
			*removed++
			if mode == "remove-failed" {
				return nil, errors.New("受控清理未知")
			}
			return nil, nil
		}
		return boundary.execute(ctx, duration, metadata, args...)
	}
}

// assertFixtureDiscardOutcome 同时核实完整归属、仅一次删除与未知结果保留。
func assertFixtureDiscardOutcome(t *testing.T, mode string, removed int, err error) {
	t.Helper()
	if (mode == "valid") != (err == nil) || mode == "labels" && removed != 0 || mode != "labels" && removed != 1 {
		t.Fatal("缓存发布失败清理没有遵守完整归属", err, removed)
	}
	if err != nil {
		var fault *Fault
		if !errors.As(err, &fault) || !fault.Unknown {
			t.Fatal("清理未确认没有保留未知状态")
		}
	}
}

// TestFixtureCachePrerequisiteFailure 任一原前置边界失败均不加载复用检查，也不调用编译命令。
func TestFixtureCachePrerequisiteFailure(t *testing.T) {
	for _, fault := range []string{"mod download", "disconnect", "network-still-present", "go version", "version-mismatch", "gofmt", "mod verify"} {
		t.Run(fault, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			original := checkFailureCommand(boundary, fault)
			boundary.driver.execute = func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
				command := strings.Join(args, " ")
				if strings.Contains(command, "go vet") || strings.Contains(command, "go test") || strings.Contains(command, "go build") {
					t.Fatal("失败后仍进入重阶段")
				}
				return original(ctx, duration, metadata, args...)
			}
			work := execution{driver: boundary.driver, box: boundary.box, report: func(context.Context, Step) (bool, error) { return true, nil }}
			material := project.SourceMaterial{Files: []project.SourceFile{{Path: "cmd/service/main.go"}}}
			if err := work.checkFixtureMaterial(context.Background(), boundary.container.ID, material, nil); err == nil {
				t.Fatal("失败前置边界允许复用")
			}
		})
	}
}
