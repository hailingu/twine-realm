// 本文件恢复原操作，不以新目标绕过未知外部命令。
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// RetryProjectRun 仅失败且已结清外部动作的当前原操作允许显式重试。
func (s *Store) RetryProjectRun(ctx context.Context, id, key string) (projectrun.Receipt, error) {
	return runtimeWrite(ctx, s.db, func(tx *sql.Tx) (projectrun.Receipt, error) {
		return retryProjectRunLocked(ctx, tx, id, key)
	})
}

// retryProjectRunLocked 在持有项目事务后完成单一状态转换。
func retryProjectRunLocked(ctx context.Context, tx *sql.Tx, id, key string) (projectrun.Receipt, error) {
	if err := lockBuildProject(ctx, tx, id); err != nil {
		return projectrun.Receipt{}, err
	}
	receipt, _, err := readProjectRunReceipt(ctx, tx, key)
	if err != nil {
		return receipt, err
	}
	if receipt.Target.ProjectID != id {
		return projectrun.Receipt{}, project.Failure("MUTATION_NOT_FOUND", "未找到项目原请求")
	}
	current, _, err := readProjectRunLease(ctx, tx, id)
	if err != nil {
		return receipt, err
	}
	if current.Operation.ID != receipt.OperationID {
		return receipt, project.Failure("PROJECT_RUN_CONFLICT", "原操作不再是当前目标")
	}
	if current.Operation.State == "accepted" || current.Operation.State == "executing" || current.Operation.State == "waiting_retry" {
		receipt.Replayed = true
		return receipt, nil
	}
	if unsettledProjectRun(&current.Operation) || current.Operation.State == "needs_attention" {
		return receipt, project.Failure("PROJECT_RUN_UNKNOWN", "原外部动作未知，禁止重试")
	}
	if current.Operation.State != "failed" {
		return receipt, project.Failure("PROJECT_RUN_CONFLICT", "当前原操作不能重试")
	}
	current.Operation.State = "waiting_retry"
	current.Operation.Code = "RETRY_REQUESTED"
	current.Operation.Message = "已接受原操作重试"
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_operations SET lease_until=? WHERE id=?", time.Now().UTC(), current.Operation.ID); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projectrun_instances SET observation_epoch=observation_epoch+1 WHERE project_id=?", id); err != nil {
		return receipt, err
	}
	return receipt, saveProjectRunOperation(ctx, tx, &current.Operation)
}
