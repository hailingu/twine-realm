// 本文件只发布固定分类，外部日志、SQL和秘密均不进入API或事件。
package servicedeploy

import (
	"errors"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// Fault 区分有证据的失败与必须核实原结果的未知，不持有原始工具输出。
type Fault struct {
	Code, Message string
	Unknown       bool
}

// Error 仅返回固定安全文案。
func (fault *Fault) Error() string { return fault.Message }

// Failure 为受信运行适配器创建稳定安全诊断。
func Failure(code, message string, unknown bool) error { return &Fault{code, message, unknown} }

// Diagnostic 保留原领域分类但不传播任何外部错误原文。
func Diagnostic(err error) (string, string, bool) {
	var local *Fault
	if errors.As(err, &local) {
		return local.Code, local.Message, local.Unknown
	}
	var runtime *mysqlruntime.Fault
	if errors.As(err, &runtime) {
		return runtime.Code, runtime.Message, runtime.Unknown
	}
	var build *servicebuild.Fault
	if errors.As(err, &build) {
		return build.Code, build.Message, build.Unknown
	}
	var docker *localdocker.Fault
	if errors.As(err, &docker) {
		return docker.Code, docker.Message, docker.Unknown
	}
	var files *privatefiles.Error
	if errors.As(err, &files) {
		return "DEPLOY_" + files.Code, files.Message, files.Unknown
	}
	var domain *project.Error
	if errors.As(err, &domain) {
		return domain.Code, domain.Message, false
	}
	return "DEPLOY_RESULT_UNKNOWN", "外部结果或持久检查点尚未确认，保留原资源等待核实", true
}

// domainError 将安全分类交给已有HTTP边界，不把外部cause序列化。
func domainError(err error) error {
	code, message, _ := Diagnostic(err)
	return project.Failure(code, message)
}
