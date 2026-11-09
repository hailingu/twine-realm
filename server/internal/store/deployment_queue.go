// 本文件以持久租约与项目行锁围栏执行结果，过期领取首先核实旧外部事实。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// ClaimDeployment 先只读找候选，再在项目锁内复核当前指针和期限。
func (store *Store) ClaimDeployment(ctx context.Context, now, until time.Time) (*servicedeploy.Lease, error) {
	if !until.After(now) || until.Sub(now) > 10*time.Minute {
		return nil, fmt.Errorf("部署租约期限无效")
	}
	var id string
	err := store.db.QueryRowContext(ctx, `SELECT o.project_id FROM deployment_operations o JOIN deployment_instances i ON i.current_operation_id=o.id WHERE (o.state='accepted' OR (o.state='executing' AND (o.lease_until IS NULL OR o.lease_until<=?))) AND NOT EXISTS (SELECT 1 FROM projectrun_instances p WHERE p.project_id=i.project_id AND (p.desired<>'running' OR p.state<>'succeeded')) ORDER BY o.created_at,o.id LIMIT 1`, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (*servicedeploy.Lease, error) { return claimDeploymentLocked(ctx, tx, id, now, until) })
}

// claimDeploymentLocked 失效执行不能继续结算，即使旧操作在外部稍后完成。
func claimDeploymentLocked(ctx context.Context, tx *sql.Tx, id string, now, until time.Time) (*servicedeploy.Lease, error) {
	if err := lockBuildProject(ctx, tx, id); err != nil {
		return nil, err
	}
	if err := projectRunPermission(ctx, tx, id); err != nil {
		return nil, err
	}
	instance, err := readDeploymentInstance(ctx, tx, id, true)
	if err != nil || instance == nil {
		return nil, err
	}
	row, err := readDeploymentOperation(ctx, tx, id, instance.serviceID, instance.operationID)
	if err != nil {
		return nil, err
	}
	if servicedeploy.Terminal(row.value.State) || row.value.State != "accepted" && row.lease.Valid && row.lease.Time.After(now) {
		return nil, nil
	}
	if row.execution == math.MaxUint64 {
		return nil, fmt.Errorf("部署执行代次耗尽")
	}
	target, err := readDeploymentTarget(ctx, tx, row.value.TargetID)
	if err != nil {
		return nil, err
	}
	if target.Generation != strconv.FormatUint(instance.generation, 10) {
		return nil, fmt.Errorf("部署领取指针不一致")
	}
	recovering := row.value.State != "accepted" || row.execution != 0
	row.value.State, row.value.Code, row.value.Message = "executing", "EXECUTING", "正在核实固定部署，尚未确认业务就绪"
	if _, err := tx.ExecContext(ctx, "UPDATE deployment_operations SET execution=execution+1,lease_until=? WHERE id=?", until, row.value.ID); err != nil {
		return nil, err
	}
	if err := saveDeploymentOperation(ctx, tx, &row.value, "claimed"); err != nil {
		return nil, err
	}
	return &servicedeploy.Lease{Target: target, Operation: row.value, Execution: row.execution + 1, Recover: recovering}, nil
}

// DeploymentAuthorized 供外部命令期间只读核实当前执行许可。
func (store *Store) DeploymentAuthorized(ctx context.Context, lease servicedeploy.Lease) (bool, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		_, valid, err := lockedDeploymentLease(ctx, tx, lease)
		return valid, err
	})
}

// lockedDeploymentLease 所有写入都按项目、实例、操作的相同顺序核实围栏。
func lockedDeploymentLease(ctx context.Context, tx *sql.Tx, lease servicedeploy.Lease) (deploymentOperationRow, bool, error) {
	var empty deploymentOperationRow
	if err := lockBuildProject(ctx, tx, lease.Target.ProjectID); err != nil {
		return empty, false, err
	}
	if err := projectRunPermission(ctx, tx, lease.Target.ProjectID); err != nil {
		return empty, false, err
	}
	instance, err := readDeploymentInstance(ctx, tx, lease.Target.ProjectID, true)
	if err != nil || instance == nil {
		return empty, false, err
	}
	if instance.operationID != lease.Operation.ID || instance.serviceID != lease.Target.ServiceID || strconv.FormatUint(instance.generation, 10) != lease.Target.Generation {
		return empty, false, nil
	}
	row, err := readDeploymentOperation(ctx, tx, lease.Target.ProjectID, lease.Target.ServiceID, lease.Operation.ID)
	valid := err == nil && row.value.TargetID == lease.Target.ID && row.execution == lease.Execution && row.value.State == "executing" && row.lease.Valid && row.lease.Time.After(time.Now())
	return row, valid, err
}

// CheckpointDeployment 在每个外部阶段之前和之后持久记录，不让旧执行继续下一步。
func (store *Store) CheckpointDeployment(ctx context.Context, lease servicedeploy.Lease, point servicedeploy.Step) (bool, error) {
	if !servicedeploy.ValidStep(point) {
		return false, fmt.Errorf("部署检查点不符合固定集合")
	}
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		row, valid, err := lockedDeploymentLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		found := false
		for index, item := range row.value.Steps {
			if item.Name == point.Name {
				row.value.Steps[index] = point
				found = true
				break
			}
		}
		if !found {
			row.value.Steps = append(row.value.Steps, point)
		}
		return true, saveDeploymentOperation(ctx, tx, &row.value, "checkpoint")
	})
}

// FinishDeployment 只接受有真实认证、迁移完整性和HTTP就绪的成功，未知不能抹除旧事实。
func (store *Store) FinishDeployment(ctx context.Context, lease servicedeploy.Lease, result servicedeploy.Completion) error {
	if err := servicedeploy.ValidateCompletion(result); err != nil {
		return err
	}
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		row, valid, err := lockedDeploymentLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		row.value.State, row.value.Code, row.value.Message = result.State, result.Code, result.Message
		settleDeploymentSteps(&row.value, result)
		if err := saveDeploymentOperation(ctx, tx, &row.value, "finished"); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE deployment_operations SET lease_until=NULL WHERE id=?", row.value.ID); err != nil {
			return false, err
		}
		code := ""
		if result.State != "succeeded" {
			code = result.Code
		}
		return true, saveDeploymentObservation(ctx, tx, lease.Target, result.Observation, code, time.Now().UTC())
	})
	return err
}

// settleDeploymentSteps 步骤使用自己的终态词汇，未知操作结果不得破坏公开步骤契约。
func settleDeploymentSteps(operation *servicedeploy.Operation, result servicedeploy.Completion) {
	state := result.State
	if state == "needs_attention" {
		state = "unknown"
	}
	for i, step := range operation.Steps {
		if step.State == "executing" {
			operation.Steps[i].State, operation.Steps[i].Code, operation.Steps[i].UpdatedAt = state, result.Code, mysqlruntime.Timestamp(time.Now())
		}
	}
}

// saveDeploymentOperation 公开版本与索引状态及事件同事务推进。
func saveDeploymentOperation(ctx context.Context, tx *sql.Tx, op *servicedeploy.Operation, kind string) error {
	version, err := strconv.ParseUint(op.Version, 10, 64)
	if err != nil || version == math.MaxUint64 {
		return fmt.Errorf("部署操作版本不能继续")
	}
	op.Version, op.UpdatedAt = strconv.FormatUint(version+1, 10), mysqlruntime.Timestamp(time.Now())
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE deployment_operations SET state=?,version=?,payload=? WHERE id=?", op.State, op.Version, data, op.ID); err != nil {
		return err
	}
	return appendDeploymentEvent(ctx, tx, *op, kind, data)
}

// appendDeploymentEvent 保存永久部署证明，短期项目SSE另用同事务独立序号。
func appendDeploymentEvent(ctx context.Context, tx *sql.Tx, op servicedeploy.Operation, kind string, data []byte) error {
	if len(data) > 65536 {
		return fmt.Errorf("部署事件超过固定上限")
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, "SELECT sequence FROM deployment_instances WHERE project_id=?", op.ProjectID).Scan(&sequence); err != nil {
		return err
	}
	if sequence == math.MaxUint64 {
		return fmt.Errorf("部署事件序列耗尽")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE deployment_instances SET sequence=sequence+1 WHERE project_id=?", op.ProjectID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO deployment_events(project_id,sequence,operation_id,kind,payload,created_at) VALUES(?,?,?,?,?,?)", op.ProjectID, sequence+1, op.ID, kind, data, time.Now().UTC())
	if err != nil {
		return err
	}
	return appendDomainStateEvent(ctx, tx, op.ProjectID, "deployment", op.ID)
}
