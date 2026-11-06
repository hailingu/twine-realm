// 本文件对环境与私有根边界注入真实形态故障，不用默认值放行未知工具。
package servicebuild

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEnvironmentCommand 只模拟工具读取，所有未声明命令都失败。
func fakeEnvironmentCommand(ctx context.Context, _ time.Duration, _ bool, args ...string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	command := strings.Join(args, " ")
	switch command {
	case "context show":
		return []byte("local-test\n"), nil
	case "context inspect local-test":
		return []byte(`[{"Endpoints":{"docker":{"Host":"unix:///tmp/local-test.sock","SkipTLSVerify":false}}}]`), nil
	case "--context local-test info --format {{json .}}":
		return []byte(`{"ID":"test-daemon","OSType":"linux","OperatingSystem":"test","ServerVersion":"29.9.2","Architecture":"aarch64"}`), nil
	case "--context local-test buildx version":
		return []byte("github.com/docker/buildx v0.38.1 public-test\n"), nil
	case "--context local-test buildx inspect local-test":
		return []byte("Name: local-test\nDriver: docker\nNodes:\nName: local-test\nEndpoint: local-test\nStatus: running\nBuildKit version: v0.34.0\nLabels:\n"), nil
	case "--context local-test image inspect " + ToolchainImage:
		return json.Marshal([]any{map[string]any{"ID": "sha256:" + strings.Repeat("a", 64), "Architecture": "arm64", "Os": "linux", "RepoDigests": []string{"golang@" + strings.SplitN(ToolchainImage, "@", 2)[1]}, "Config": map[string]any{"Env": []string{"GOLANG_VERSION=1.27.3"}}}})
	default:
		return nil, errors.New("未声明工具读取")
	}
}

// testBuildEnvironment 每个测试都经过实际解析得到基础环境，而非绕过环境核实。
func testBuildEnvironment(t *testing.T) Environment {
	t.Helper()
	value, err := discover(context.Background(), fakeEnvironmentCommand, "")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// assertBuildFault 检查机器分类，不通过自然语言匹配掩盖边界变化。
func assertBuildFault(t *testing.T, err error, code string) {
	t.Helper()
	actual, _, _ := diagnostic(err)
	if err == nil || actual != code {
		t.Fatalf("期望%s，实际%v / %s", code, err, actual)
	}
}

// TestEnvironmentDiscoveryBoundaries 每一个外部读取缺失或畸形均不得开放构建。
func TestEnvironmentDiscoveryBoundaries(t *testing.T) {
	base := testBuildEnvironment(t)
	if !validEnvironment(base) || base.UID != os.Getuid() || base.BuilderName != base.Context {
		t.Fatal("合法环境未冻结完整身份")
	}
	for _, phase := range []string{"context show", "context inspect", "info", "buildx version", "buildx inspect", "image inspect"} {
		t.Run(phase, func(t *testing.T) {
			for _, corrupt := range []bool{false, true} {
				rejectEnvironmentRead(t, phase, corrupt)
			}
		})
	}
}

// TestRemoteAndBuilderRejection 远程socket、额外节点、非docker-driver和失配digest均明确拒绝。
func TestRemoteAndBuilderRejection(t *testing.T) {
	for _, endpoint := range []string{"tcp://remote.invalid:2375", "ssh://remote.invalid", "unix:///tmp/socket?override=1", "unix://remote/tmp/socket"} {
		_, _, err := localContext(context.Background(), func(context.Context, time.Duration, bool, ...string) ([]byte, error) {
			return []byte(`[{"Endpoints":{"docker":{"Host":"` + endpoint + `"}}}]`), nil
		}, "local-test")
		assertBuildFault(t, err, "BUILD_CONTEXT_UNSUPPORTED")
	}
	if _, _, err := localContext(context.Background(), fakeEnvironmentCommand, "--remote"); err == nil {
		t.Fatal("参数形态context被放行")
	}
	valid := "Name: local-test\nDriver: docker\nName: local-test\nEndpoint: local-test\nStatus: running\nBuildKit version: v0.34.0\n"
	for _, value := range []string{strings.Replace(valid, "Driver: docker", "Driver: remote", 1), strings.Replace(valid, "Endpoint: local-test", "Endpoint: remote", 1), strings.Replace(valid, "Status: running", "Status: stopped", 1), valid + "Name: second\n", strings.Replace(valid, "v0.34.0", "unknown", 1)} {
		if _, err := localBuilderVersion(value, "local-test"); err == nil {
			t.Fatal("不明确builder被放行")
		}
	}
}

// TestPrivateBuildInstallation 原身份损坏、链接、权限改变和环境漂移都不得自动初始化替代。
func TestPrivateBuildInstallation(t *testing.T) {
	environment := testBuildEnvironment(t)
	path := filepath.Join(t.TempDir(), "build")
	if err := InitializeDirectory(path, nil, environment); err != nil {
		t.Fatal(err)
	}
	driver, err := OpenDriver(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := driver.InstallationID()
	driver.execute = fakeEnvironmentCommand
	if err := driver.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitializeDirectory(path, nil, environment); err == nil {
		t.Fatal("初始化覆盖原目录")
	}
	driver, err = OpenDriver(path, nil)
	if err != nil || driver.InstallationID() != identity {
		t.Fatal("重开丢失安装身份")
	}
	driver.execute = func(ctx context.Context, timeout time.Duration, metadata bool, args ...string) ([]byte, error) {
		data, err := fakeEnvironmentCommand(ctx, timeout, metadata, args...)
		return []byte(strings.ReplaceAll(string(data), "29.9.2", "29.9.3")), err
	}
	assertBuildFault(t, driver.Preflight(context.Background()), "BUILD_ENVIRONMENT_CHANGED")
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	material := filepath.Join(path, "installation.json")
	if err := os.WriteFile(material, []byte(`{"schemaVersion":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDriver(path, nil); err == nil {
		t.Fatal("损坏安装材料被静默恢复")
	}
	if err := os.Remove(material); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(path, "missing"), material); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDriver(path, nil); err == nil {
		t.Fatal("链接安装材料被放行")
	}
}

// rejectEnvironmentRead 每次只破坏一个真实工具返回边界。
func rejectEnvironmentRead(t *testing.T, phase string, corrupt bool) {
	t.Helper()
	_, err := discover(context.Background(), func(ctx context.Context, timeout time.Duration, metadata bool, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), phase) {
			if corrupt {
				return []byte("{}"), nil
			}
			return nil, errors.New("受控不可用")
		}
		return fakeEnvironmentCommand(ctx, timeout, metadata, args...)
	}, "")
	if err == nil {
		t.Fatal("未知环境被开放")
	}
}
