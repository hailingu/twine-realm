// 本文件以项目锁和执行代次保护父账本，未知外部命令不会因租约失效而重跑。
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

	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// readProjectRunLease 只在已经持有项目锁时读取父执行身份。
func readProjectRunLease(ctx context.Context, tx *sql.Tx, id string) (lease projectrun.Lease, until sql.NullTime, err error) {
	var target, data []byte
	err = tx.QueryRowContext(ctx, `SELECT o.target_json,o.payload,o.execution,o.lease_until FROM projectrun_instances i JOIN projectrun_operations o ON o.id=i.current_operation_id WHERE i.project_id=?`, id).Scan(&target, &data, &lease.Execution, &until)
	if err != nil {
		return
	}
	if err = decodeRuntimeJSON(target, &lease.Target); err != nil {
		return
	}
	err = decodeRuntimeJSON(data, &lease.Operation)
	return
}

// saveProjectRunOperation 同事务保存操作版本、当前状态与通知。
func saveProjectRunOperation(ctx context.Context, tx *sql.Tx, op *projectrun.Operation) error {
	version, err := strconv.ParseUint(op.Version, 10, 64)
	if err != nil || version == math.MaxUint64 {
		return fmt.Errorf("父操作版本无效")
	}
	op.Version = strconv.FormatUint(version+1, 10)
	op.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_operations SET payload=? WHERE id=?", data, op.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_instances SET state=? WHERE project_id=? AND current_operation_id=?", op.State, op.ProjectID, op.ID); err != nil {
		return err
	}
	return appendProjectRunEvent(ctx, tx, *op, data)
}

// ClaimProjectRun 过期执行在持久边界接续，在途外部动作一律转未知。
func (s *Store) ClaimProjectRun(ctx context.Context, now, until time.Time) (*projectrun.Lease, error) {
	if !until.After(now) || until.Sub(now) > 10*time.Minute {
		return nil, fmt.Errorf("父租约期限无效")
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT i.project_id FROM projectrun_instances i JOIN projectrun_operations o ON o.id=i.current_operation_id WHERE i.state='accepted' OR (i.state='waiting_retry' AND o.lease_until<=?) OR (i.state='executing' AND o.lease_until<=?) ORDER BY o.created_at,o.id LIMIT 1`, now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runtimeWrite(ctx, s.db, func(tx *sql.Tx) (*projectrun.Lease, error) {
		return claimProjectRunLocked(ctx, tx, id, now, until)
	})
}

// unsettledProjectRun 将尚无完成证明的动作永久标记未知。
func unsettledProjectRun(op *projectrun.Operation) bool {
	unknown := false
	for i := range op.Steps {
		if op.Steps[i].State == "executing" || op.Steps[i].State == "unknown" {
			op.Steps[i].State = "unknown"
			unknown = true
		}
	}
	return unknown
}

// lockProjectRunLease 对旧执行及过期租约返回无许可。
func lockProjectRunLease(ctx context.Context, tx *sql.Tx, lease projectrun.Lease) (projectrun.Lease, bool, error) {
	if err := lockBuildProject(ctx, tx, lease.Target.ProjectID); err != nil {
		return projectrun.Lease{}, false, err
	}
	current, expiry, err := readProjectRunLease(ctx, tx, lease.Target.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return current, false, nil
	}
	valid := err == nil && current.Operation.ID == lease.Operation.ID && current.Target.Generation == lease.Target.Generation && current.Execution == lease.Execution && current.Operation.State == "executing" && expiry.Valid && expiry.Time.After(time.Now())
	return current, valid, err
}

// ProjectRunAuthorized 不允许旧worker在父状态变化后继续写外部资源。
func (s *Store) ProjectRunAuthorized(ctx context.Context, lease projectrun.Lease) (bool, error) {
	return runtimeWrite(ctx, s.db, func(tx *sql.Tx) (bool, error) {
		_, valid, err := lockProjectRunLease(ctx, tx, lease)
		return valid, err
	})
}

// CheckpointProjectRun 在每个外部动作前后提交，不允许未知动作被重置。
func (s *Store) CheckpointProjectRun(ctx context.Context, lease projectrun.Lease, step projectrun.Step) (bool, error) {
	if step.Name == "" || len(step.Name) > 80 || len(step.Code) > 80 || len(step.ExternalID) > 128 || (step.State != "executing" && step.State != "succeeded" && step.State != "unknown") {
		return false, fmt.Errorf("父检查点无效")
	}
	return runtimeWrite(ctx, s.db, func(tx *sql.Tx) (bool, error) {
		return checkpointProjectRunLocked(ctx, tx, lease, step)
	})
}

// YieldProjectRun 退出只释放已知边界，在途命令保留未知。
func (s *Store) YieldProjectRun(ctx context.Context, lease projectrun.Lease) error {
	_, err := runtimeWrite(ctx, s.db, func(tx *sql.Tx) (bool, error) {
		current, valid, err := lockProjectRunLease(ctx, tx, lease)
		if err != nil || !valid {
			return false, err
		}
		current.Operation.State = "accepted"
		if unsettledProjectRun(&current.Operation) {
			current.Operation.State = "needs_attention"
			current.Operation.Code = "PROJECT_RUN_UNKNOWN"
		}
		return true, saveProjectRunOperation(ctx, tx, &current.Operation)
	})
	return err
}

// claimProjectRunLocked 在持有项目事务后完成单一状态转换。
func claimProjectRunLocked(ctx context.Context, tx *sql.Tx, id string, now, until time.Time) (*projectrun.Lease, error) {
	if err := lockBuildProject(ctx, tx, id); err != nil {
		return nil, err
	}
	lease, expiry, err := readProjectRunLease(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if lease.Operation.State != "accepted" && (lease.Operation.State != "executing" && lease.Operation.State != "waiting_retry" || !expiry.Valid || expiry.Time.After(now)) {
		return nil, nil
	}
	if unsettledProjectRun(&lease.Operation) {
		lease.Operation.State = "needs_attention"
		lease.Operation.Code = "PROJECT_RUN_UNKNOWN"
		lease.Operation.Message = "旧外部命令结果未知，禁止反向动作"
		return nil, saveProjectRunOperation(ctx, tx, &lease.Operation)
	}
	if lease.Execution == math.MaxUint64 {
		return nil, fmt.Errorf("父执行代次耗尽")
	}
	lease.Recover = lease.Execution > 0
	lease.Execution++
	lease.Operation.State = "executing"
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_operations SET execution=?,lease_until=? WHERE id=?", lease.Execution, until, lease.Operation.ID); err != nil {
		return nil, err
	}
	if err = saveProjectRunOperation(ctx, tx, &lease.Operation); err != nil {
		return nil, err
	}
	return &lease, nil
}

// checkpointProjectRunLocked 在持有项目事务后完成单一状态转换。
func checkpointProjectRunLocked(ctx context.Context, tx *sql.Tx, lease projectrun.Lease, step projectrun.Step) (bool, error) {
	current, valid, err := lockProjectRunLease(ctx, tx, lease)
	if err != nil || !valid {
		return false, err
	}
	if err := validateProjectRunStep(current, step); err != nil {
		return false, err
	}
	step.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := updateProjectRunStep(&current.Operation, step); err != nil {
		return false, err
	}
	return true, saveProjectRunOperation(ctx, tx, &current.Operation)
}

// updateProjectRunStep 保持已结算动作不可重写，新动作先登记执行意图。
func updateProjectRunStep(operation *projectrun.Operation, step projectrun.Step) error {
	found := false
	for i, old := range operation.Steps {
		if old.Name == step.Name {
			if old.State == "unknown" || old.State == "succeeded" || old.ExternalID != step.ExternalID {
				return fmt.Errorf("已结算或未知父动作不能改写")
			}
			operation.Steps[i] = step
			found = true
			break
		}
	}
	if !found {
		if step.State != "executing" {
			return fmt.Errorf("外部动作必须先持久登记")
		}
		operation.Steps = append(operation.Steps, step)
	}
	return nil
}
