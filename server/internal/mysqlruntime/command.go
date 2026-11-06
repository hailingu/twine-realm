// 本文件保留MySQL诊断与命令入口，实际进程隔离由共享端口负责。
package mysqlruntime

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"io"
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

// dockerCommand 是可信适配器的测试接缝，不接受用户shell或环境。
type dockerCommand func(context.Context, ...string) ([]byte, error)

// localDocker 沿用已发布的45秒期限与错误语义，组合受控本机命令端口。
func localDocker(ctx context.Context, args ...string) ([]byte, error) {
	data, err := localdocker.Run(ctx, localdocker.Policy{Timeout: 45 * time.Second}, args...)
	var fault *localdocker.Fault
	if errors.As(err, &fault) {
		return nil, failure(fault.Code, fault.Message, fault.Retryable, fault.Unknown)
	}
	return data, err
}

// joinClose 同时保留业务失败与资源关闭失败，不让清理覆盖原始结果。
func joinClose(err *error, closer io.Closer) { *err = errors.Join(*err, closer.Close()) }
