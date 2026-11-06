// 本文件把执行边界诊断映射到领域错误，不回显工具日志、宿主路径或秘密。
package servicebuild

import (
	"errors"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// diagnostic 为多层包装错误保留稳定分类，未知执行失败保守要求核实。
func diagnostic(err error) (string, string, bool) {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault.Code, fault.Message, fault.Unknown
	}
	var docker *localdocker.Fault
	if errors.As(err, &docker) {
		return docker.Code, docker.Message, docker.Unknown
	}
	var files *privatefiles.Error
	if errors.As(err, &files) {
		return "BUILD_" + files.Code, files.Message, files.Unknown
	}
	var documents *engineering.Error
	if errors.As(err, &documents) {
		return documents.Code, documents.Message, false
	}
	var source *project.Error
	if errors.As(err, &source) {
		return source.Code, source.Message, false
	}
	return "BUILD_RESULT_UNKNOWN", "构建结果或持久检查点未确认，保留原材料等待核实", true
}

// domainError 将适配器固定诊断交给现有本机HTTP边界。
func domainError(err error) error {
	code, message, _ := diagnostic(err)
	return project.Failure(code, message)
}
