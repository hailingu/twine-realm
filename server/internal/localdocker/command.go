// Package localdocker 提供本机Docker受控命令端口，不向业务代码发放宿主socket或凭据。
package localdocker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Policy 仅允许消费者选择有界期限与固定Buildx诊断选项，不接受任意宿主环境注入。
type Policy struct {
	Timeout       time.Duration
	BuildMetadata bool
}

// Fault 只保存稳定诊断与可重试性质，原始 Docker 输出不进入 API 或日志。
type Fault struct {
	Code      string
	Message   string
	Retryable bool
	Unknown   bool
	Output    []byte `json:"-"`
}

// Error 返回不含 Docker 输出、凭据和宿主路径的诊断。
func (fault *Fault) Error() string { return fault.Message }

// failure 统一建立适配器边界诊断，不以字符串判别外部错误。
func failure(code, message string, retryable, unknown bool) *Fault {
	return &Fault{Code: code, Message: message, Retryable: retryable, Unknown: unknown}
}

// boundedBuffer 允许排空子进程输出但不保留无界数据，超限仍按失败处理。
type boundedBuffer struct {
	data     bytes.Buffer
	exceeded bool
}

// Write 保留最多1MiB，禁止第三方输出耗尽控制面内存。
func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	const limit = 1024 * 1024
	n := len(value)
	if n > limit-buffer.data.Len() {
		value = value[:max(0, limit-buffer.data.Len())]
		buffer.exceeded = true
	}
	if _, err := buffer.data.Write(value); err != nil {
		return 0, err
	}
	return n, nil
}

// Run 只继承固定工具位置与显式元数据策略，不继承业务凭据或执行用户shell。
func Run(ctx context.Context, policy Policy, args ...string) ([]byte, error) {
	if policy.Timeout <= 0 || policy.Timeout > 10*time.Minute {
		return nil, failure("DOCKER_POLICY_INVALID", "Docker命令必须具有不超过十分钟的明确期限", false, false)
	}
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		if os.Getenv(key) != "" {
			return nil, failure("DOCKER_OVERRIDE_UNSUPPORTED", "本切片不接受Docker环境覆盖，请用本地context明确初始化", false, false)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if value, present := os.LookupEnv(key); present {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if policy.BuildMetadata {
		command.Env = append(command.Env, "BUILDX_METADATA_WARNINGS=true", "BUILDX_METADATA_PROVENANCE=min")
	}
	command.Stdin = nil
	command.WaitDelay = 2 * time.Second
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, failure("DOCKER_OUTPUT_INVALID", "Docker 返回超过受控输出上限", false, true)
	}
	if ctx.Err() != nil {
		return nil, commandFailure("DOCKER_RESULT_UNKNOWN", "Docker 命令中断，须先核实原资源", &stdout, &stderr)
	}
	if err != nil {
		return nil, commandFailure("DOCKER_UNAVAILABLE", "Docker 命令未确认，须核实连接、权限与原资源", &stdout, &stderr)
	}
	if hasWarning(stderr.data.String()) {
		return nil, failure("DOCKER_WARNING", "Docker 返回告警，需处理后再核实", false, true)
	}
	return stdout.data.Bytes(), nil
}

// commandFailure 保留有界私有诊断；Error和JSON均不回显这些字节。
func commandFailure(code, message string, stdout, stderr *boundedBuffer) *Fault {
	value := failure(code, message, true, true)
	value.Output = append(append([]byte{}, stdout.data.Bytes()...), stderr.data.Bytes()...)
	return value
}

// hasWarning 把外部工具标准告警保持为失败，不过滤或降级它们。
func hasWarning(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "warning:") || strings.Contains(value, "warn:") || strings.Contains(value, "warn[") || strings.Contains(value, "level=warning") || strings.Contains(value, "level=warn")
}
