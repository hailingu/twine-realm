// 本文件拥有构建操作的同库版本、取消与事件，外部结果不能越过执行围栏。
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

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// lockBuildProject 保持所有构建写入相同锁序，不改变画布revision。
func lockBuildProject(ctx context.Context, tx *sql.Tx, id string) error {
	var found string
	err := tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=? FOR UPDATE", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Failure("PROJECT_NOT_FOUND", "项目不存在")
	}
	return err
}

// saveBuildOperation 在同事务推进公开版本和事件，SQL索引状态不能落后JSON。
func saveBuildOperation(ctx context.Context, tx *sql.Tx, op *servicebuild.Operation, kind string) error {
	version, err := strconv.ParseUint(op.Version, 10, 64)
	if err != nil || version == math.MaxUint64 {
		return fmt.Errorf("构建操作版本不可继续推进")
	}
	op.Version, op.UpdatedAt = strconv.FormatUint(version+1, 10), servicebuild.Timestamp(time.Now())
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE build_operations SET state=?,version=?,payload=? WHERE id=?", op.State, op.Version, data, op.ID); err != nil {
		return err
	}
	return appendBuildEvent(ctx, tx, *op, kind, data)
}

// CancelBuild 先持久接受取消，响应丢失仍可按独立原键核实，不伪报沙箱已停止。
func (store *Store) CancelBuild(ctx context.Context, id, serviceID, operationID string, command servicebuild.CancelCommand, hash [32]byte) (servicebuild.Receipt, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (servicebuild.Receipt, error) {
		return buildReceiptWrite(ctx, tx, command.MutationID, hash, func() (servicebuild.Receipt, error) {
			return cancelBuildLocked(ctx, tx, id, serviceID, operationID, command)
		})
	})
}

// cancelBuildLocked 允许明确取消未知原操作，以核实和收回其资源后解除阻塞。
func cancelBuildLocked(ctx context.Context, tx *sql.Tx, id, serviceID, operationID string, command servicebuild.CancelCommand) (servicebuild.Receipt, error) {
	var receipt servicebuild.Receipt
	if err := lockBuildProject(ctx, tx, id); err != nil {
		return receipt, err
	}
	instance, err := readBuildInstance(ctx, tx, id, serviceID, true)
	if err != nil {
		return receipt, err
	}
	if instance == nil || instance.operationID != operationID {
		return receipt, project.Failure("BUILD_OPERATION_CONFLICT", "取消只接受此服务当前原操作")
	}
	row, err := readBuildOperation(ctx, tx, id, serviceID, operationID)
	if err != nil {
		return receipt, err
	}
	observed, parseErr := strconv.ParseUint(command.BaseVersion, 10, 64)
	current, versionErr := strconv.ParseUint(row.value.Version, 10, 64)
	if parseErr != nil || versionErr != nil || observed > current {
		return receipt, project.Failure("BUILD_VERSION_CONFLICT", "取消引用了未观察到的操作版本，请核实原操作")
	}
	if row.value.State == "succeeded" || row.value.State == "cancelled" || row.value.State == "failed" {
		return receipt, project.Failure("BUILD_NOT_CANCELLABLE", "该构建已结束，未执行新的取消")
	}
	row.value.CancelRequested, row.value.State, row.value.Code, row.value.Message = true, "cancelling", "CANCEL_ACCEPTED", "取消已持久接受，沙箱停止仍须实际核实"
	if err := saveBuildOperation(ctx, tx, &row.value, "cancel_accepted"); err != nil {
		return receipt, err
	}
	target, err := readBuildTarget(ctx, tx, row.value.TargetID)
	return servicebuild.Receipt{SchemaVersion: 1, MutationID: command.MutationID, Kind: "cancelBuild", OperationID: operationID, Target: target, Version: row.value.Version}, err
}
