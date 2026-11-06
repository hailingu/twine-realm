// 本文件保留MySQL项目锁入口，完整文件归属和flock边界由共享私有根拥有。
package mysqlruntime

import "github.com/hailingu/twine-realm/server/internal/project"

// projectLock 同一安装根的同项目共用稳定锁文件，不把旧租约失效视为进程退出。
func (driver *Driver) projectLock(id string) (func() error, error) {
	if !project.ValidID(id) {
		return nil, failure("RUNTIME_PLAN_INVALID", "项目运行身份无效", false, false)
	}
	release, err := driver.files.Lock("locks/" + id + ".lock")
	return release, privateFileFailure(err)
}
