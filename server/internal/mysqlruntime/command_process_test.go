// 本文件用独占假 Docker 可执行文件验证真实子进程边界，不访问任何 Docker daemon。
package mysqlruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// isolatedDockerCommand 只在测试进程设置 PATH，证明宿主其他环境不会传入外部工具。
func isolatedDockerCommand(t *testing.T, script string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("TWINE_TEST_PRIVATE_SENTINEL", "host-only")
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		t.Setenv(name, "")
	}
}

// TestDockerCommandProcessBoundaries 真实命令告警、非零、超限和中断都不能冒充已确认成功。
func TestDockerCommandProcessBoundaries(t *testing.T) {
	cases := []struct {
		name, script, code string
		cancelled          bool
	}{
		{"标准告警", "printf 'warning: controlled\\n' >&2; printf 'untrusted'", "DOCKER_WARNING", false},
		{"工具失败", "printf 'untrusted' >&2; exit 7", "DOCKER_UNAVAILABLE", false},
		{"输出超限", "/usr/bin/head -c 1048577 /dev/zero", "DOCKER_OUTPUT_INVALID", false},
		{"结果中断", "printf 'untrusted'", "DOCKER_RESULT_UNKNOWN", true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			isolatedDockerCommand(t, item.script)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if item.cancelled {
				cancel()
			}
			data, err := localDocker(ctx, "--version")
			privateFailure(t, err, item.code)
			if len(data) != 0 {
				t.Fatal("失败命令暴露了原始不可信结果")
			}
		})
	}
}

// TestDockerCommandDoesNotInheritPrivateEnvironment 运行适配器只继承列出的工具位置。
func TestDockerCommandDoesNotInheritPrivateEnvironment(t *testing.T) {
	isolatedDockerCommand(t, `printf '%s' "${TWINE_TEST_PRIVATE_SENTINEL-absent}"`)
	data, err := localDocker(context.Background(), "--version")
	if err != nil || string(data) != "absent" {
		t.Fatal("外部命令继承了未允许的宿主环境")
	}
}
