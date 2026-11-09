// 本文件同事务结算父结果及相关组件事实，停止事实不伪造认证时间。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// FinishProjectRun 成功必须有完整事实且不存在未结外部动作。
func (s *Store) FinishProjectRun(ctx context.Context, lease projectrun.Lease, result projectrun.Completion) error {
	_, err := runtimeWrite(ctx, s.db, func(tx *sql.Tx) (bool, error) {
		return finishProjectRunLocked(ctx, tx, lease, result)
	})
	return err
}

// validateProjectRunFacts 确认停止与恢复各自的可观察证据。
func validateProjectRunFacts(target projectrun.Target, f *projectrun.Facts) error {
	if f == nil {
		return fmt.Errorf("父操作缺少核实事实")
	}
	if target.Desired == "stopped" {
		for _, stamp := range []string{f.ApplicationObservedAt, f.DatabaseObservedAt} {
			observed, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || observed.After(time.Now().Add(5*time.Second)) {
				return fmt.Errorf("停止事实时间无效")
			}
		}
		if !f.ApplicationStopped || !f.DatabaseStopped || f.ApplicationObservedAt == "" || f.DatabaseObservedAt == "" {
			return fmt.Errorf("停止事实不完整")
		}
		return nil
	}
	if f.Runtime == nil || f.Deployment == nil || !f.Runtime.Ready || !f.Deployment.Ready || f.Runtime.ServerUUID != target.Frozen.ServerUUID || f.Deployment.DatabaseUUID != target.Frozen.ServerUUID || f.Runtime.ContainerID != target.Frozen.DatabaseContainerID || f.Deployment.ContainerID != target.Frozen.ApplicationContainerID {
		return fmt.Errorf("恢复原资源就绪证明不完整")
	}
	return nil
}

// saveProjectRunFacts 仅真实采样更新组件；停止时保留原认证与迁移证明时间。
func saveProjectRunFacts(ctx context.Context, tx *sql.Tx, lease projectrun.Lease, f *projectrun.Facts) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_instances SET facts=? WHERE project_id=?", data, lease.Target.ProjectID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if lease.Target.Desired == "stopped" {
		if f.DatabaseStopped {
			if _, err = tx.ExecContext(ctx, "UPDATE runtime_instances SET observation_code='PROJECT_STOPPED',last_attempt_at=? WHERE project_id=?", now, lease.Target.ProjectID); err != nil {
				return err
			}
		}
		if f.ApplicationStopped {
			_, err = tx.ExecContext(ctx, "UPDATE deployment_instances SET observation_code='PROJECT_STOPPED',last_attempt_at=? WHERE project_id=?", now, lease.Target.ProjectID)
		}
		return err
	}
	if err := saveProjectRunRuntimeFact(ctx, tx, lease, f, now); err != nil {
		return err
	}
	if f.Deployment != nil {
		return saveDeploymentObservation(ctx, tx, lease.Target.Frozen.Deployment, f.Deployment, "", now)
	}
	return nil
}

// finishProjectRunLocked 在持有项目事务后完成单一状态转换。
func finishProjectRunLocked(ctx context.Context, tx *sql.Tx, lease projectrun.Lease, result projectrun.Completion) (bool, error) {
	current, valid, err := lockProjectRunLease(ctx, tx, lease)
	if err != nil || !valid {
		return false, err
	}
	unknown := unsettledProjectRun(&current.Operation)
	if unknown {
		result.State = "needs_attention"
		result.Code = "PROJECT_RUN_UNKNOWN"
		result.Message = "外部命令结果未知，保留现场"
	}
	if err = validateProjectRunCompletion(current, result); err != nil {
		return false, err
	}
	if result.Facts != nil {
		if err = saveProjectRunFacts(ctx, tx, current, result.Facts); err != nil {
			return false, err
		}
	}
	if result.State == "waiting_retry" {
		if _, err = tx.ExecContext(ctx, "UPDATE projectrun_operations SET lease_until=? WHERE id=?", time.Now().UTC().Add(30*time.Second), current.Operation.ID); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_instances SET observation_epoch=observation_epoch+1 WHERE project_id=?", current.Target.ProjectID); err != nil {
		return false, err
	}
	current.Operation.State, current.Operation.Code, current.Operation.Message = result.State, result.Code, result.Message
	return true, saveProjectRunOperation(ctx, tx, &current.Operation)
}

// saveProjectRunRuntimeFact 只持久化原数据库身份相符的认证事实。
func saveProjectRunRuntimeFact(ctx context.Context, tx *sql.Tx, lease projectrun.Lease, f *projectrun.Facts, now time.Time) error {
	if f.Runtime != nil {
		instance, err := readRuntimeInstance(ctx, tx, lease.Target.ProjectID, true)
		if err != nil {
			return err
		}
		if instance == nil {
			return fmt.Errorf("原运行实例缺失")
		}
		conflict, err := recordRuntimeObservation(ctx, tx, instance, f.Runtime, "", now)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("原数据库身份不一致")
		}
	}
	return nil
}

// validateProjectRunCompletion 先校验结算语义再产生数据库写入。
func validateProjectRunCompletion(current projectrun.Lease, result projectrun.Completion) (err error) {
	if result.State != "succeeded" && result.State != "failed" && result.State != "needs_attention" && result.State != "waiting_retry" {
		return fmt.Errorf("父操作终态无效")
	}
	if result.State == "succeeded" {
		if err = completeProjectRunSteps(current); err != nil {
			return err
		}
		if err = validateProjectRunFacts(current.Target, result.Facts); err != nil {
			return err
		}
	}
	return nil
}
