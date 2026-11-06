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
func (store *Store) RuntimeDeployment(ctx context.Context, id string) (*servicedeploy.RuntimeMaterial, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (*servicedeploy.RuntimeMaterial, error) {
		instance, err := readDeploymentInstance(ctx, tx, id, false)
		if err != nil || instance == nil {
			return nil, err
		}
		row, err := readDeploymentOperation(ctx, tx, id, instance.serviceID, instance.operationID)
		if err != nil {
			return nil, err
		}
		target, err := readDeploymentTarget(ctx, tx, row.value.TargetID)
		if err != nil {
			return nil, err
		}
		var materialized bool
		// 同一操作重新领取可覆盖步骤；不可变事件保留先前已经完成材料的正面证明。
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_events e JOIN deployment_operations o ON e.operation_id=o.id AND e.project_id=o.project_id JOIN deployment_targets t ON t.id=o.target_id WHERE e.project_id=? AND o.service_id=? AND t.payload->>'$.plan.runtimeDigest'=? AND JSON_CONTAINS(e.payload,?))`, id, target.ServiceID, target.Plan.RuntimeDigest, `{"steps":[{"name":"material","state":"succeeded"}]}`).Scan(&materialized)
		return &servicedeploy.RuntimeMaterial{Target: target, Materialized: materialized}, err
	})
}
