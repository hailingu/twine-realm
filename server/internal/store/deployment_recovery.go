// 本文件在控制面正常退出时释放原租约，不假定外部迁移已退出或创建新目标。
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// YieldDeployment 只让当前执行可被重新领取，重启先进入原资源核实。
func (store *Store) YieldDeployment(ctx context.Context, lease servicedeploy.Lease) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		row, valid, err := lockedDeploymentLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		row.value.Code, row.value.Message = "DEPLOY_INTERRUPTED", "控制面退出，重启后先核实原任务、锁与迁移历史"
		if err := saveDeploymentOperation(ctx, tx, &row.value, "interrupted"); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE deployment_operations SET lease_until=? WHERE id=?", time.Now().UTC(), row.value.ID)
		return true, err
	})
	return err
}

// RuntimeDeployment 返回原Compose扩展的已接受目标，执行中也必须属于同一原项目。
func (store *Store) RuntimeDeployment(ctx context.Context, id string) (*servicedeploy.Target, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (*servicedeploy.Target, error) {
		instance, err := readDeploymentInstance(ctx, tx, id, false)
		if err != nil || instance == nil {
			return nil, err
		}
		row, err := readDeploymentOperation(ctx, tx, id, instance.serviceID, instance.operationID)
		if err != nil {
			return nil, err
		}
		target, err := readDeploymentTarget(ctx, tx, row.value.TargetID)
		return &target, err
	})
}
