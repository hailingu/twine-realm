// 本文件只核实原操作材料并收回归属明确的沙箱，不在重启后重跑业务代码。
package servicebuild

import (
	"context"
	"errors"
	"reflect"
)

// Recover 使用同一跨进程文件锁；旧进程尚未退出时不能接管其副作用。
func (driver *Driver) Recover(ctx context.Context, target Target, operationID string) (err error) {
	if err := driver.validateTarget(target, operationID); err != nil {
		return err
	}
	unlock, err := driver.files.Lock("locks/" + operationID + ".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := driver.Preflight(ctx); err != nil {
		return err
	}
	box := driver.newSandbox(target, operationID)
	container, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return err
	}
	if container == nil {
		return driver.cleanupSandbox(ctx, box)
	}
	data, err := driver.files.Read("operations/"+operationID+"/target.json", 65536)
	if err != nil {
		return err
	}
	var stored Target
	if decode(data, &stored) != nil || !reflect.DeepEqual(stored, target) {
		return failure("BUILD_MATERIAL_CONFLICT", "原构建材料身份不符，保留原沙箱等待核实")
	}
	return driver.cleanupSandbox(ctx, box)
}
