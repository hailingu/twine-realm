// 本文件在项目行锁内冻结原目标并原子登记父操作和原键回执。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// AcceptProjectRun 不在受理事务内访问Docker。
func (s *Store) AcceptProjectRun(ctx context.Context, in projectrun.AcceptedInput) (projectrun.Receipt, error) {
	return runtimeWrite(ctx, s.db, func(tx *sql.Tx) (projectrun.Receipt, error) {
		return acceptProjectRunLocked(ctx, tx, in)
	})
}

// validateProjectRunInput 在项目锁内重新核实原组件和未结外部动作。
func validateProjectRunInput(ctx context.Context, tx *sql.Tx, in projectrun.AcceptedInput, old projectrun.Snapshot) error {
	if in.Frozen.DatabaseContainerID == "" || in.Frozen.ApplicationContainerID == "" || in.Frozen.MigrationContainerID == "" || in.Frozen.ServerUUID == "" {
		return project.Failure("PROJECT_RUN_TARGET_CHANGED", "原资源身份不完整")
	}
	if err := validatePreviousProjectRun(in, old); err != nil {
		return err
	}
	runtime, err := readRuntimeSnapshot(ctx, tx, in.ProjectID)
	if err != nil {
		return err
	}
	deployment, err := readDeploymentSnapshot(ctx, tx, in.ProjectID, in.Frozen.Deployment.ServiceID)
	if err != nil {
		return err
	}
	if runtime.Target == nil || deployment.Target == nil || runtime.Operation == nil || deployment.Operation == nil || runtime.Operation.State != "succeeded" || deployment.Operation.State != "succeeded" || runtime.Target.Desired != "running" {
		return project.Failure("PROJECT_RUN_BUSY", "原组件操作尚未成功结算")
	}
	// 观察代次不是目标内容，比较前仅去除内部采样令牌。
	rt, dt := *runtime.Target, *deployment.Target
	rt.ObservationEpoch = ""
	dt.ObservationEpoch = ""
	frozen := in.Frozen
	frozen.Runtime.ObservationEpoch = ""
	frozen.Deployment.ObservationEpoch = ""
	if !reflect.DeepEqual(rt, frozen.Runtime) || !reflect.DeepEqual(dt, frozen.Deployment) {
		return project.Failure("PROJECT_RUN_TARGET_CHANGED", "原组件目标已经改变")
	}
	if err := validateSettledProjectComponents(runtime, deployment); err != nil {
		return err
	}
	if in.Command.Desired == "stopped" {
		if err := validateProjectRunProof(in.Frozen, in.Proof, time.Now().UTC()); err != nil {
			return project.Failure("PROJECT_RUN_BUSY", err.Error())
		}
	}
	return nil
}

// insertProjectRun 原子登记不可变目标、操作、通知及回执。
func insertProjectRun(ctx context.Context, tx *sql.Tx, in projectrun.AcceptedInput, previous string) (projectrun.Receipt, error) {
	var receipt projectrun.Receipt
	generation, err := strconv.ParseUint(previous, 10, 64)
	if err != nil || generation == math.MaxUint64 {
		return receipt, fmt.Errorf("父目标代次无效")
	}
	id, err := project.NewID()
	if err != nil {
		return receipt, err
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	next := strconv.FormatUint(generation+1, 10)
	target := projectrun.Target{ProjectID: in.ProjectID, Generation: next, Desired: in.Command.Desired, Frozen: in.Frozen, CreatedAt: stamp}
	operation := projectrun.Operation{ID: id, ProjectID: in.ProjectID, MutationID: in.Command.MutationID, Generation: next, Version: "1", State: "accepted", Code: "ACCEPTED", Message: "已接受原项目目标", Steps: []projectrun.Step{}, CreatedAt: stamp, UpdatedAt: stamp}
	data, err := json.Marshal(operation)
	if err != nil {
		return receipt, err
	}
	targetData, err := json.Marshal(target)
	if err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO projectrun_operations(id,project_id,generation,payload,target_json,created_at) VALUES(?,?,?,?,?,?)", id, in.ProjectID, next, data, targetData, now); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO projectrun_instances(project_id,generation,current_operation_id,desired,state) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE observation_epoch=observation_epoch+1,generation=VALUES(generation),current_operation_id=VALUES(current_operation_id),desired=VALUES(desired),state=VALUES(state)`, in.ProjectID, next, id, in.Command.Desired, "accepted"); err != nil {
		return receipt, err
	}
	if err = appendProjectRunEvent(ctx, tx, operation, data); err != nil {
		return receipt, err
	}
	receipt = projectrun.Receipt{SchemaVersion: 1, MutationID: in.Command.MutationID, OperationID: id, Target: target}
	response, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE projectrun_receipts SET response=? WHERE id=?", response, in.Command.MutationID)
	return receipt, err
}

// appendProjectRunEvent 保留永久父账本并使用旧协议已有的项目失效通知。
func appendProjectRunEvent(ctx context.Context, tx *sql.Tx, op projectrun.Operation, data []byte) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO projectrun_events(project_id,operation_id,payload,created_at) VALUES(?,?,?,?)", op.ProjectID, op.ID, data, time.Now().UTC()); err != nil {
		return err
	}
	return appendStateEvent(ctx, tx, op.ProjectID, "project", nil)
}

// acceptProjectRunLocked 在持有项目事务后完成单一状态转换。
func acceptProjectRunLocked(ctx context.Context, tx *sql.Tx, in projectrun.AcceptedInput) (projectrun.Receipt, error) {
	_, err := tx.ExecContext(ctx, "INSERT INTO projectrun_receipts(id,intent_hash,created_at) VALUES(?,?,?)", in.Command.MutationID, in.Hash[:], time.Now().UTC())
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		value, hash, readErr := readProjectRunReceipt(ctx, tx, in.Command.MutationID)
		if readErr == nil && hash != in.Hash {
			return value, project.Failure("IDEMPOTENCY_CONFLICT", "原键内容不同")
		}
		value.Replayed = true
		return value, readErr
	}
	if err != nil {
		return projectrun.Receipt{}, err
	}
	if err = lockRevision(ctx, tx, in.ProjectID, in.Command.BaseRevision); err != nil {
		return projectrun.Receipt{}, err
	}
	previous, err := readProjectRunSnapshot(ctx, tx, in.ProjectID)
	if err != nil {
		return projectrun.Receipt{}, err
	}
	if previous.Generation != in.Command.BaseGeneration {
		return projectrun.Receipt{}, project.Failure("PROJECT_RUN_CONFLICT", "父目标代次已改变")
	}
	if err = validateProjectRunInput(ctx, tx, in, previous); err != nil {
		return projectrun.Receipt{}, err
	}
	return insertProjectRun(ctx, tx, in, previous.Generation)
}

// validatePreviousProjectRun 保持停止和恢复各自的父状态前置条件。
func validatePreviousProjectRun(in projectrun.AcceptedInput, old projectrun.Snapshot) error {
	if in.Command.Desired == "running" {
		if old.Target == nil || old.Target.Desired != "stopped" || old.Operation == nil || old.Operation.State != "succeeded" || old.Facts == nil || !old.Facts.ApplicationStopped || !old.Facts.DatabaseStopped || !reflect.DeepEqual(old.Target.Frozen, in.Frozen) {
			return project.Failure("PROJECT_RUN_NOT_STOPPED", "原项目尚未确认完整停止")
		}
	} else if old.Operation != nil && (old.Operation.State != "succeeded" || old.Target.Desired != "running") {
		return project.Failure("PROJECT_RUN_BUSY", "先核实原父操作")
	}
	return nil
}

// validateSettledProjectComponents 拒绝仍有未结外部动作的组件历史。
func validateSettledProjectComponents(runtime mysqlruntime.Snapshot, deployment servicedeploy.Snapshot) error {
	for _, step := range runtime.Operation.Steps {
		if step.State == "executing" || step.State == "unknown" {
			return project.Failure("PROJECT_RUN_BUSY", "原数据库外部动作尚未结算")
		}
	}
	for _, step := range deployment.Operation.Steps {
		if step.State == "executing" || step.State == "unknown" {
			return project.Failure("PROJECT_RUN_BUSY", "原业务外部动作尚未结算")
		}
	}
	return nil
}
