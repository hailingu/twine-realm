// 本文件注入工具失败与断网假成功，验证流水线停止在真实失败步骤而不继续执行后续业务代码。
package servicebuild

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// checkFailureCommand 只模拟固定检查输出；资源读取仍由完整归属夹具验证。
func checkFailureCommand(boundary *recoveryBoundary, fault string) dockerCommand {
	return func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "network disconnect") {
			if fault == "disconnect" {
				return nil, errors.New("受控断网失败")
			}
			if fault != "network-still-present" {
				boundary.container.NetworkSettings.Networks = map[string]any{}
			}
			return nil, nil
		}
		if len(args) > 2 && args[2] == "exec" {
			return checkToolOutput(boundary, fault, command)
		}
		return boundary.execute(ctx, duration, metadata, args...)
	}
}

// checkToolOutput 不执行命令，只返回固定工具的受控成功或失败，断网前禁止下载之外的检查。
func checkToolOutput(boundary *recoveryBoundary, fault, command string) ([]byte, error) {
	if !strings.Contains(command, "mod download") && len(boundary.container.NetworkSettings.Networks) != 0 {
		return nil, errors.New("禁止在仍联网的沙箱内执行业务检查")
	}
	if strings.Contains(command, fault) {
		return nil, errors.New("受控检查失败")
	}
	if strings.HasSuffix(command, "go version") {
		if fault == "version-mismatch" {
			return []byte("go version unverified"), nil
		}
		return []byte("go version go1.27.3 linux/" + boundary.box.target.Environment.Architecture), nil
	}
	return nil, nil
}

// TestChecksStopAtUnconfirmedBoundary 固定检查、实际工具链和网络核实任一失败都不能记成功或进入后续步骤。
func TestChecksStopAtUnconfirmedBoundary(t *testing.T) {
	cases := []struct{ fault, step string }{
		{"mod download", "dependencies"}, {"disconnect", "offline"}, {"network-still-present", "offline"},
		{"go version", "toolchain"}, {"version-mismatch", "toolchain"}, {"gofmt", "format"},
		{"mod verify", "module"}, {"go vet", "vet"}, {"go test", "test"}, {"go build", "build"},
	}
	for _, input := range cases {
		t.Run(input.fault, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			boundary.driver.execute = checkFailureCommand(boundary, input.fault)
			steps := []Step{}
			work := execution{driver: boundary.driver, box: boundary.box, report: func(_ context.Context, step Step) (bool, error) { steps = append(steps, step); return true, nil }}
			material := project.SourceMaterial{Files: []project.SourceFile{{Path: "cmd/service/main.go"}}}
			if err := work.check(context.Background(), boundary.container.ID, material); err == nil {
				t.Fatal("失败检查被视为成功")
			}
			if len(steps) == 0 || steps[len(steps)-1].Name != input.step || steps[len(steps)-1].State != "executing" {
				t.Fatalf("失败后错误推进检查：%v", steps)
			}
			for _, checked := range work.checks {
				if checked.Name == input.step {
					t.Fatal("未确认步骤发布了成功证明")
				}
			}
		})
	}
}

// TestNoGoFilesCannotPassFormatting 源码材料缺失时连格式检查都不能凭空通过。
func TestNoGoFilesCannotPassFormatting(t *testing.T) {
	boundary := newRecoveryBoundary(t)
	work := execution{driver: boundary.driver, box: boundary.box}
	_, err := work.format(context.Background(), boundary.container.ID, project.SourceMaterial{})
	assertBuildFault(t, err, "BUILD_GO_SOURCE_MISSING")
}
