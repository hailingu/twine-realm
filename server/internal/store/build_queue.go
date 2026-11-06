// 本文件以持久租约和执行代次拥有构建，不因平台重启盲重跑任意业务代码。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// ClaimBuild 先只读找候选，再按项目锁复核；过期执行仅进入资源恢复。
func (store *Store) ClaimBuild(ctx context.Context, now, until time.Time) (*servicebuild.Lease, error) {
	if !until.After(now) || until.Sub(now) > 10*time.Minute {
		return nil, fmt.Errorf("构建租约必须具有不超过十分钟的明确期限")
	}
	var id, serviceID string
	err := store.db.QueryRowContext(ctx, `SELECT o.project_id,o.service_id FROM build_operations o JOIN build_instances i ON i.current_operation_id=o.id WHERE o.state='accepted' OR (o.state IN ('executing','cancelling') AND (o.lease_until IS NULL OR o.lease_until<=?)) ORDER BY o.created_at,o.id LIMIT 1`, now).Scan(&id, &serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (*servicebuild.Lease, error) {
		return claimBuildLocked(ctx, tx, id, serviceID, now, until)
	})
}

// claimBuildLocked 所有领取都重新核实当前指针，旧终态不能被重复执行。
func claimBuildLocked(ctx context.Context, tx *sql.Tx, id, serviceID string, now, until time.Time) (*servicebuild.Lease, error) {
	if err := lockBuildProject(ctx, tx, id); err != nil {
		return nil, err
	}
	instance, err := readBuildInstance(ctx, tx, id, serviceID, true)
	if err != nil || instance == nil {
		return nil, err
	}
	row, err := readBuildOperation(ctx, tx, id, serviceID, instance.operationID)
	if err != nil {
		return nil, err
	}
	if servicebuild.Terminal(row.value.State) || row.value.State != "accepted" && row.lease.Valid && row.lease.Time.After(now) {
		return nil, nil
	}
	if row.execution == math.MaxUint64 {
		return nil, fmt.Errorf("构建执行代次已耗尽，需保留现场核实")
	}
	target, err := readBuildTarget(ctx, tx, row.value.TargetID)
	if err != nil {
		return nil, err
	}
	if target.Generation != strconv.FormatUint(instance.generation, 10) {
		return nil, fmt.Errorf("构建领取目标不一致")
	}
	recovering := row.value.State != "accepted" || row.execution != 0
	row.value.State, row.value.Code, row.value.Message = "executing", "EXECUTING", "固定输入构建已领取，检查尚未完成"
	if row.value.CancelRequested {
		row.value.State = "cancelling"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE build_operations SET execution=execution+1,lease_until=? WHERE id=?", until, row.value.ID); err != nil {
		return nil, err
	}
	if err := saveBuildOperation(ctx, tx, &row.value, "claimed"); err != nil {
		return nil, err
	}
	return &servicebuild.Lease{Target: target, Operation: row.value, Execution: row.execution + 1, Recover: recovering}, nil
}

// BuildAuthorized 在长命令期间只读取当前围栏，取消不会等待整个命令期限。
func (store *Store) BuildAuthorized(ctx context.Context, lease servicebuild.Lease) (bool, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM build_operations o JOIN build_instances i ON i.current_operation_id=o.id WHERE o.id=? AND o.project_id=? AND o.service_id=? AND o.execution=? AND o.state='executing' AND o.lease_until>?`, lease.Operation.ID, lease.Target.ProjectID, lease.Target.ServiceID, lease.Execution, time.Now().UTC()).Scan(&count)
	return count == 1, err
}

// lockedBuildLease 以同一锁序核实围栏，迟到结果只能无写入返回。
func lockedBuildLease(ctx context.Context, tx *sql.Tx, lease servicebuild.Lease) (buildOperationRow, bool, error) {
	var empty buildOperationRow
	if err := lockBuildProject(ctx, tx, lease.Target.ProjectID); err != nil {
		return empty, false, err
	}
	instance, err := readBuildInstance(ctx, tx, lease.Target.ProjectID, lease.Target.ServiceID, true)
	if err != nil || instance == nil {
		return empty, false, err
	}
	if instance.operationID != lease.Operation.ID || strconv.FormatUint(instance.generation, 10) != lease.Target.Generation {
		return empty, false, nil
	}
	row, err := readBuildOperation(ctx, tx, lease.Target.ProjectID, lease.Target.ServiceID, lease.Operation.ID)
	valid := err == nil && row.execution == lease.Execution && row.value.TargetID == lease.Target.ID && !servicebuild.Terminal(row.value.State) && row.lease.Valid && row.lease.Time.After(time.Now())
	return row, valid, err
}

// CheckpointBuild 在执行两侧持久记录，取消或失效的旧执行不能继续。
func (store *Store) CheckpointBuild(ctx context.Context, lease servicebuild.Lease, point servicebuild.Step) (bool, error) {
	if !servicebuild.ValidStep(point) {
		return false, fmt.Errorf("构建检查点不符合固定步骤契约")
	}
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		row, valid, err := lockedBuildLease(ctx, tx, lease)
		if err != nil || !valid || row.value.CancelRequested {
			return false, err
		}
		replaceBuildStep(&row.value, point)
		return true, saveBuildOperation(ctx, tx, &row.value, "checkpoint")
	})
}

// replaceBuildStep 只替换同名步骤，不丢失其他已确认检查。
func replaceBuildStep(op *servicebuild.Operation, point servicebuild.Step) {
	for index, step := range op.Steps {
		if step.Name == point.Name {
			op.Steps[index] = point
			return
		}
	}
	op.Steps = append(op.Steps, point)
}

// FinishBuild 只有当前执行且沙箱已收回的结果可进入终态，取消优先于迟到成功。
func (store *Store) FinishBuild(ctx context.Context, lease servicebuild.Lease, completion servicebuild.Completion) error {
	if err := servicebuild.ValidateCompletion(lease, completion); err != nil {
		return err
	}
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) { return finishBuildLocked(ctx, tx, lease, completion) })
	return err
}

// finishBuildLocked 在同一事务校验围栏、结清操作和事件，最后发布成功候选。
func finishBuildLocked(ctx context.Context, tx *sql.Tx, lease servicebuild.Lease, completion servicebuild.Completion) (bool, error) {
	row, valid, err := lockedBuildLease(ctx, tx, lease)
	if err != nil || !valid {
		return false, err
	}
	if row.value.CancelRequested && completion.State != "needs_attention" {
		completion = servicebuild.Completion{State: "cancelled", Code: "CANCELLED", Message: "原沙箱已核实收回，未发布新候选"}
	}
	row.value.State, row.value.Code, row.value.Message, row.value.Candidate = completion.State, completion.Code, completion.Message, completion.Candidate
	settleBuildSteps(&row.value, completion)
	if err := saveBuildOperation(ctx, tx, &row.value, "finished"); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE build_operations SET lease_until=NULL WHERE id=?", row.value.ID); err != nil {
		return false, err
	}
	if err := saveBuildCandidatePointer(ctx, tx, lease, row.value.ID, completion.Candidate); err != nil {
		return false, err
	}
	return true, nil
}

// settleBuildSteps 保留已确认检查，只把在途步骤结清为真实终态。
func settleBuildSteps(op *servicebuild.Operation, completion servicebuild.Completion) {
	for index, step := range op.Steps {
		if step.State == "executing" {
			op.Steps[index].State, op.Steps[index].Code, op.Steps[index].UpdatedAt = completion.State, completion.Code, servicebuild.Timestamp(time.Now())
		}
	}
}

// saveBuildCandidatePointer 只在同一成功事务发布本次候选指针，失败不抹除历史。
func saveBuildCandidatePointer(ctx context.Context, tx *sql.Tx, lease servicebuild.Lease, id string, candidate *servicebuild.Candidate) error {
	if candidate == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, "UPDATE build_instances SET candidate_operation_id=? WHERE project_id=? AND service_id=?", id, lease.Target.ProjectID, lease.Target.ServiceID)
	return err
}
