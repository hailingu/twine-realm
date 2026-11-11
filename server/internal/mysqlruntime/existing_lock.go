// 本文件为聚合生命周期提供唯一的原项目文件锁拥有者。
package mysqlruntime

import (
	"context"
	"errors"
)

// WithExistingProject 串行拥有原运行项目锁，回调只可调用不再获取该锁的严格适配方法。
func (driver *Driver) WithExistingProject(ctx context.Context, projectID string, action func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := driver.projectLock(projectID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return action()
}
