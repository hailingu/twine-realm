// 本文件用持久租约与执行代次领取操作；外部资源写入还须持有项目文件锁。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
)

// ClaimRuntime 先找候选再按项目锁复核，租约只在同库条件成立时推进。
func (store *Store) ClaimRuntime(ctx context.Context, now, leaseUntil time.Time) (*mysqlruntime.Lease, error) {
	var id string
	err := store.db.QueryRowContext(ctx, `SELECT i.project_id FROM runtime_instances i JOIN runtime_operations o ON o.id=i.current_operation_id WHERE ((o.state IN ('accepted','waiting_retry') AND o.next_attempt<=?) OR (o.state='executing' AND o.lease_until<=?)) ORDER BY o.created_at,o.id LIMIT 1`, now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (*mysqlruntime.Lease, error) { return claimRuntimeLocked(ctx, tx, id, now, leaseUntil) })
}

// claimRuntimeLocked 所有运行写入统一先锁实例再锁定原操作所属信息。
func claimRuntimeLocked(ctx context.Context, tx *sql.Tx, id string, now, leaseUntil time.Time) (*mysqlruntime.Lease, error) {
	instance, err := readRuntimeInstance(ctx, tx, id, true)
	if err != nil || instance == nil {
		return nil, err
	}
	op, err := readRuntimeOperation(ctx, tx, id, instance.operationID)
	if err != nil {
		return nil, err
	}
	if !runtimeDue(op, now) {
		return nil, nil
	}
	if op.value.Attempts >= 6 || op.execution == math.MaxUint64 {
		return nil, exhaustRuntime(ctx, tx, instance, now)
	}
	if !leaseUntil.After(now) {
		return nil, fmt.Errorf("运行领取期限无效")
	}
	target, err := readRuntimeTarget(ctx, tx, id, strconv.FormatUint(instance.generation, 10))
	if err != nil {
		return nil, err
	}
	if err := matchRuntimeTarget(instance, target); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_steps SET state='unknown',code='RECOVERING',updated_at=? WHERE operation_id=? AND state='executing'", now, op.value.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_operations SET state='executing',version=version+1,attempts=attempts+1,execution=execution+1,lease_until=?,code='',message='',updated_at=? WHERE id=?", leaseUntil, now, op.value.ID); err != nil {
		return nil, err
	}
	if err := appendRuntimeEvent(ctx, tx, id, op.value.ID, "execution_claimed", "EXECUTING", now); err != nil {
		return nil, err
	}
	current, err := readRuntimeOperation(ctx, tx, id, op.value.ID)
	if err != nil {
		return nil, err
	}
	return &mysqlruntime.Lease{Target: target, Operation: current.value, Execution: strconv.FormatUint(current.execution, 10), VolumeConfirmed: instance.volumeConfirmed, ServerUUID: instance.serverUUID}, nil
}

// runtimeDue 不把未到期执行或终态作为可恢复任务。
func runtimeDue(op runtimeOperationRow, now time.Time) bool {
	switch op.value.State {
	case "accepted", "waiting_retry":
		return !op.nextAttempt.After(now)
	case "executing":
		return op.lease.Valid && !op.lease.Time.After(now)
	default:
		return false
	}
}

// exhaustRuntime 对最后一次崩溃同样保留未知步骤，不通过无限领取掩盖耗尽。
func exhaustRuntime(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, now time.Time) error {
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_operations SET state='needs_attention',version=version+1,code='RETRY_EXHAUSTED',message='恢复尝试已耗尽，保留原资源并等待明确核实',lease_until=NULL,updated_at=? WHERE id=?", now, instance.operationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_steps SET state='unknown',code='RETRY_EXHAUSTED',updated_at=? WHERE operation_id=? AND state='executing'", now, instance.operationID); err != nil {
		return err
	}
	return appendRuntimeEvent(ctx, tx, instance.projectID, instance.operationID, "needs_attention", "RETRY_EXHAUSTED", now)
}

// matchRuntimeTarget 核对历史目标与不可变实例，不能让损坏载荷指向另一资源。
func matchRuntimeTarget(instance *runtimeInstanceRow, target mysqlruntime.Target) error {
	left, err := mysqlruntime.Fingerprint(instance.resource)
	if err != nil {
		return err
	}
	right, err := mysqlruntime.Fingerprint(target.Resource)
	if err != nil {
		return err
	}
	if target.ProjectID != instance.projectID || target.Generation != strconv.FormatUint(instance.generation, 10) || left != right {
		return fmt.Errorf("运行目标与实例归属不一致")
	}
	return nil
}

// CheckpointRuntime 副作用前复核目标，副作用后可记录原操作结果但不推进新目标。
func (store *Store) CheckpointRuntime(ctx context.Context, lease mysqlruntime.Lease, point mysqlruntime.Checkpoint) (bool, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		instance, op, valid, err := lockRuntimeLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		current := runtimeLeaseCurrent(instance, op, lease)
		if point.State == "check" {
			return current, nil
		}
		if point.State == "executing" && !current {
			return false, nil
		}
		return current, saveRuntimeCheckpoint(ctx, tx, instance, lease, point)
	})
}

// saveRuntimeCheckpoint 在同一事务保存步骤、单调卷证据、操作版本和事件，任何失败一并回滚。
func saveRuntimeCheckpoint(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, lease mysqlruntime.Lease, point mysqlruntime.Checkpoint) error {
	if len(point.Code) > 80 || len(point.ExternalID) > 128 {
		return fmt.Errorf("运行检查点诊断超出边界")
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, "UPDATE runtime_steps SET state=?,code=?,external_id=?,updated_at=? WHERE operation_id=? AND name=?", point.State, point.Code, point.ExternalID, now, lease.Operation.ID, point.Name)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.Join(fmt.Errorf("运行步骤未登记"), err)
	}
	if point.VolumeConfirmed {
		if _, err := tx.ExecContext(ctx, "UPDATE runtime_instances SET volume_confirmed=TRUE WHERE project_id=? AND resource_id=?", instance.projectID, lease.Target.Resource.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_operations SET version=version+1,updated_at=? WHERE id=?", now, lease.Operation.ID); err != nil {
		return err
	}
	return appendRuntimeEvent(ctx, tx, instance.projectID, lease.Operation.ID, "step_"+point.State, point.Code, now)
}

// lockRuntimeLease 不凭过期租约接受迟到写入，实例锁始终先于操作读取。
func lockRuntimeLease(ctx context.Context, tx *sql.Tx, lease mysqlruntime.Lease) (*runtimeInstanceRow, runtimeOperationRow, bool, error) {
	instance, err := readRuntimeInstance(ctx, tx, lease.Target.ProjectID, true)
	if err != nil || instance == nil {
		return nil, runtimeOperationRow{}, false, err
	}
	op, err := readRuntimeOperation(ctx, tx, instance.projectID, lease.Operation.ID)
	if err != nil {
		return instance, op, false, err
	}
	valid := strconv.FormatUint(op.execution, 10) == lease.Execution && op.lease.Valid && time.Now().Before(op.lease.Time) && (op.value.State == "executing" || op.value.State == "superseded")
	return instance, op, valid, nil
}

// runtimeLeaseCurrent 同时核对目标代次、当前指针与操作状态，不只比一个版本号。
func runtimeLeaseCurrent(instance *runtimeInstanceRow, op runtimeOperationRow, lease mysqlruntime.Lease) bool {
	return instance.operationID == lease.Operation.ID && strconv.FormatUint(instance.generation, 10) == lease.Target.Generation && op.value.State == "executing"
}

// FinishRuntime 只结算当前有效执行；旧结果不能标记新目标成功。
func (store *Store) FinishRuntime(ctx context.Context, lease mysqlruntime.Lease, completion mysqlruntime.Completion) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		instance, op, valid, err := lockRuntimeLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		if !runtimeLeaseCurrent(instance, op, lease) {
			return false, nil
		}
		if err := validateRuntimeCompletion(lease, completion); err != nil {
			return false, err
		}
		return true, finishRuntimeLocked(ctx, tx, instance, lease, completion)
	})
	return err
}

// finishRuntimeLocked 只在围栏和结算事实已通过后保存操作、最后事实与事件。
func finishRuntimeLocked(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, lease mysqlruntime.Lease, completion mysqlruntime.Completion) error {
	now := time.Now().UTC()
	observationCode := completion.Code
	if completion.State == "succeeded" {
		observationCode = ""
	}
	conflict, err := recordRuntimeObservation(ctx, tx, instance, completion.Observation, observationCode, now)
	if err != nil {
		return err
	}
	if conflict {
		completion.State, completion.Code, completion.Message = "needs_attention", "RUNTIME_DATABASE_IDENTITY_CONFLICT", "原卷的数据库身份已改变，保留现场并等待核实"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_operations SET state=?,version=version+1,code=?,message=?,next_attempt=?,lease_until=NULL,updated_at=? WHERE id=?", completion.State, completion.Code, completion.Message, completion.NextAttempt, now, lease.Operation.ID); err != nil {
		return err
	}
	if err := finishRuntimeSteps(ctx, tx, lease.Operation.ID, completion, now); err != nil {
		return err
	}
	return appendRuntimeEvent(ctx, tx, instance.projectID, lease.Operation.ID, completion.State, completion.Code, now)
}

// finishRuntimeSteps 未确认步骤保留未知与稳定原因，不把中断或耗尽改写为已成功。
func finishRuntimeSteps(ctx context.Context, tx *sql.Tx, operationID string, completion mysqlruntime.Completion, now time.Time) error {
	if completion.State == "succeeded" {
		return nil
	}
	code := completion.Code
	if completion.StepCode != "" {
		code = completion.StepCode
	}
	_, err := tx.ExecContext(ctx, "UPDATE runtime_steps SET state='unknown',code=?,updated_at=? WHERE operation_id=? AND state='executing'", code, now, operationID)
	return err
}

// validateRuntimeCompletion 不接受无事实支撑的成功或超界诊断。
func validateRuntimeCompletion(lease mysqlruntime.Lease, value mysqlruntime.Completion) error {
	if value.State != "waiting_retry" && value.State != "needs_attention" && value.State != "failed" && value.State != "succeeded" {
		return fmt.Errorf("运行结算状态无效")
	}
	if len(value.Code) > 80 || len(value.StepCode) > 80 || len(value.Message) > 256 {
		return fmt.Errorf("运行结算诊断超出边界")
	}
	if value.NextAttempt.IsZero() {
		return fmt.Errorf("运行结算缺少明确时间")
	}
	if value.State == "succeeded" {
		return validateRuntimeSuccess(lease.Target.Desired, value.Observation)
	}
	return nil
}

// validateRuntimeSuccess 成功必须有近期认证就绪或实际停止，不接受过期或未来采样。
func validateRuntimeSuccess(desired string, fact *mysqlruntime.Observation) error {
	if fact == nil {
		return fmt.Errorf("运行成功缺少近期事实")
	}
	observed, err := time.Parse(time.RFC3339Nano, fact.ObservedAt)
	if err != nil || time.Since(observed) > 20*time.Second || observed.After(time.Now().Add(5*time.Second)) {
		return fmt.Errorf("运行成功不能使用过期或未来事实")
	}
	if desired == "running" && (!fact.Running || !fact.Ready) {
		return fmt.Errorf("运行成功缺少认证就绪事实")
	}
	if desired == "stopped" && fact.Running {
		return fmt.Errorf("运行停止缺少实际停止事实")
	}
	return nil
}
