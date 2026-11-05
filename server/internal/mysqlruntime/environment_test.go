// 本文件验证外部元数据和命令边界；实际 Docker / Compose 验收另走集成检查。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnvironment 只构造不含秘密的受控安装元数据。
func testEnvironment() Environment {
	return Environment{"local", "daemon-test", "sha256:" + strings.Repeat("a", 64), "5.6.1", "native"}
}

// discoveryFixture 为精确参数返回元数据，不允许未登记命令悄悄成功。
func discoveryFixture(overrides map[string]string) dockerCommand {
	value := testEnvironment()
	responses := map[string]string{
		"context show":                             "local\n",
		"context inspect local":                    `[{"Endpoints":{"docker":{"Host":"unix:///tmp/docker.sock"}}}]`,
		"--context local info --format {{json .}}": `{"ID":"daemon-test","OSType":"linux"}`,
		"--context local compose version --short":  "5.6.1\n",
		"--context local image inspect " + Image:   `[{"Id":"` + value.ImageID + `","RepoDigests":["mysql@` + strings.Split(Image, "@")[1] + `"],"Config":{"Env":["PATH=/tools"],"Entrypoint":["docker-entrypoint.sh"],"User":""}}]`,
	}
	for key, value := range overrides {
		responses[key] = value
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, exists := responses[strings.Join(args, " ")]
		if !exists {
			return nil, errors.New("未声明测试命令")
		}
		return []byte(value), nil
	}
}

// TestDiscoveryPinAndTrustBoundary 对真实 RepoDigest 格式及不受支持环境做边界校验。
func TestDiscoveryPinAndTrustBoundary(t *testing.T) {
	value, _, err := discover(context.Background(), discoveryFixture(nil), "")
	if err != nil || value != testEnvironment() {
		t.Fatalf("固定镜像有效元数据被拒绝：%v", err)
	}
	for _, scenario := range []struct{ name, key, value string }{
		{"远程daemon", "context inspect local", `[{"Endpoints":{"docker":{"Host":"tcp://example.invalid:2375"}}}]`},
		{"非Linuxdaemon", "--context local info --format {{json .}}", `{"ID":"daemon-test","OSType":"windows"}`},
		{"不支持Compose", "--context local compose version --short", "1.29.2"},
		{"错误镜像", "--context local image inspect " + Image, `[{"Id":"sha256:other","RepoDigests":["mysql@sha256:other"]}]`},
		{"格式不完整", "context inspect local", "null"},
		{"不安全context", "context show", "--host"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, _, err := discover(context.Background(), discoveryFixture(map[string]string{scenario.key: scenario.value}), ""); err == nil {
				t.Fatal("不受支持环境被接受")
			}
		})
	}
}

// TestCommandOutputAndIsolation 命令不继承云凭据，工具告警与取消继续是失败。
func TestCommandOutputAndIsolation(t *testing.T) {
	directory := t.TempDir()
	script := `#!/bin/sh
if [ -n "$AWS_SECRET_ACCESS_KEY" ]; then exit 9; fi
case "$1" in
  warning) printf 'warning: controlled warning\n' >&2 ;;
  success) printf '{"ok":true}\n' ;;
  *) exit 7 ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-not-a-credential")
	data, err := localDocker(context.Background(), "success")
	if err != nil || !json.Valid(data) {
		t.Fatal("受控命令没有在隔离环境完成")
	}
	for _, command := range []string{"warning", "failure"} {
		if _, err := localDocker(context.Background(), command); err == nil {
			t.Fatal("工具错误或告警被吞掉")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := localDocker(ctx, "success"); err == nil {
		t.Fatal("取消命令报告成功")
	}
	var output boundedBuffer
	if _, err := output.Write(make([]byte, 1024*1024+1)); err != nil || !output.exceeded || output.data.Len() != 1024*1024 {
		t.Fatal("输出边界未生效")
	}
}
