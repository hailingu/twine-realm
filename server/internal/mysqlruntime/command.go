// 本文件将 Docker CLI 限于可信参数、有界输出和本地环境，不执行 shell。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// contextOption 保证所有外部调用都显式使用同一个已核实的Docker context。
const contextOption = "--context"

// Fault 只保存稳定诊断与可重试性质，原始 Docker 输出不进入 API 或日志。
type Fault struct {
	Code      string
	Message   string
	Retryable bool
	Unknown   bool
}

// Error 返回不含 Docker 输出、凭据和宿主路径的诊断。
func (fault *Fault) Error() string { return fault.Message }

// failure 统一建立适配器边界诊断，不以字符串判别外部错误。
func failure(code, message string, retryable, unknown bool) *Fault {
	return &Fault{code, message, retryable, unknown}
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

// dockerCommand 是可信适配器的测试接缝；不向业务代码发放 CLI 或 Docker socket。
type dockerCommand func(context.Context, ...string) ([]byte, error)

// localDocker 只继承本地工具和 Docker 配置所需位置，固定每条命令的期限。
func localDocker(ctx context.Context, args ...string) ([]byte, error) {
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		if os.Getenv(key) != "" {
			return nil, failure("DOCKER_OVERRIDE_UNSUPPORTED", "本切片不接受Docker环境覆盖，请用本地context明确初始化", false, false)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if value, present := os.LookupEnv(key); present {
			command.Env = append(command.Env, key+"="+value)
		}
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
		return nil, failure("DOCKER_RESULT_UNKNOWN", "Docker 命令中断，须先核实原资源", true, true)
	}
	if err != nil {
		return nil, failure("DOCKER_UNAVAILABLE", "Docker 命令未确认，须核实连接、权限与原资源", true, true)
	}
	if hasWarning(stderr.data.String()) {
		return nil, failure("DOCKER_WARNING", "Docker 返回告警，需处理后再核实", false, true)
	}
	return stdout.data.Bytes(), nil
}

// hasWarning 把外部工具标准告警保持为失败，不过滤或降级它们。
func hasWarning(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "warning:") || strings.Contains(value, "level=warning") || strings.Contains(value, "level=warn")
}

// joinClose 同时保留业务失败与资源关闭失败，不让清理覆盖原始结果。
func joinClose(err *error, closer io.Closer) { *err = errors.Join(*err, closer.Close()) }
