// 本文件适配共享私有目录错误，已有MySQL公开错误码不随内部复用改变。
package mysqlruntime

import (
	"errors"

	"github.com/hailingu/twine-realm/server/internal/privatefiles"
)

// privateFileFailure 只映射受信文件错误；不存在等标准原因保留原始可判别关系。
func privateFileFailure(err error) error {
	var fault *privatefiles.Error
	if !errors.As(err, &fault) {
		return err
	}
	code := fault.Code
	if code == "LOCK_BUSY" {
		code = "PROJECT_BUSY"
	}
	return failure("RUNTIME_"+code, fault.Message, fault.Retryable, fault.Unknown)
}

// privateParents 保留运行计划检查入口，共享根负责路径、类型、权限与inode核实。
func (driver *Driver) privateParents(name string) error {
	return privateFileFailure(driver.files.CheckParents(name))
}

// readPrivate 仍只接受有界0600文件，不自动修复或生成材料。
func (driver *Driver) readPrivate(name string, limit int64) ([]byte, error) {
	data, err := driver.files.Read(name, limit)
	return data, privateFileFailure(err)
}

// writeNew 独占创建，不覆盖既有文件或静默吞掉同步失败。
func (driver *Driver) writeNew(name string, data []byte) error {
	return privateFileFailure(driver.files.WriteNew(name, data))
}

// ensureMaterial 逐字节核实既有材料，不以覆盖实现配置或凭据替换。
func (driver *Driver) ensureMaterial(name string, data []byte) error {
	return privateFileFailure(driver.files.Ensure(name, data))
}
