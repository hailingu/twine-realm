// 本文件为原状态事务追加严格连续通知；不复制秘密、外部输出或完整业务载荷。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// appendStateEvent 始终在原聚合锁之后领取项目序号；回滚同时撤销序号和通知。
func appendStateEvent(ctx context.Context, tx *sql.Tx, id, scope string, reference *projectstate.Reference) error {
	var revision uint64
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM projects WHERE id=?", id).Scan(&revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO project_state_heads(project_id) VALUES(?) ON DUPLICATE KEY UPDATE project_id=project_id", id); err != nil {
		return err
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, "SELECT sequence FROM project_state_heads WHERE project_id=? FOR UPDATE", id).Scan(&sequence); err != nil {
		return err
	}
	if sequence == math.MaxUint64 {
		return fmt.Errorf("项目通知序列耗尽")
	}
	sequence++
	now := time.Now().UTC()
	event := projectstate.Event{SchemaVersion: 1, ProjectID: id, Sequence: strconv.FormatUint(sequence, 10), Scope: scope, Revision: strconv.FormatUint(revision, 10), Reference: reference, OccurredAt: projectstate.Timestamp(now)}
	event.ID = id + ":" + event.Sequence
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(data) > projectstate.DefaultPolicy().MaxFrameBytes-128 {
		return fmt.Errorf("项目通知超过固定帧边界")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE project_state_heads SET sequence=? WHERE project_id=?", sequence, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO project_state_events(project_id,sequence,payload,created_at) VALUES(?,?,?,?)", id, sequence, data, now); err != nil {
		return err
	}
	return pruneStateEvents(ctx, tx, id, sequence, now)
}

// pruneStateEvents 只裁剪连续前缀，永久 runtime/build/deployment 事件和证明保持不变。
func pruneStateEvents(ctx context.Context, tx *sql.Tx, id string, head uint64, now time.Time) error {
	policy := projectstate.DefaultPolicy()
	var countFloor uint64
	if head > uint64(policy.MaxEvents) {
		countFloor = head - uint64(policy.MaxEvents)
	}
	var floor uint64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM project_state_events WHERE project_id=? AND created_at<?", id, now.Add(-time.Duration(policy.RetentionSeconds)*time.Second)).Scan(&floor); err != nil {
		return err
	}
	if countFloor > floor {
		floor = countFloor
	}
	if floor == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM project_state_events WHERE project_id=? AND sequence<=?", id, floor); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE project_state_heads SET replay_floor=GREATEST(replay_floor,?) WHERE project_id=?", floor, id)
	return err
}

// appendDomainStateEvent 从原操作索引取得版本和执行代次，调用方不能猜造来源。
func appendDomainStateEvent(ctx context.Context, tx *sql.Tx, id, scope, operationID string) error {
	var query string
	switch scope {
	case "runtime":
		query = "SELECT '',generation,version,execution FROM runtime_operations WHERE project_id=? AND id=?"
	case "build":
		query = "SELECT o.service_id,t.generation,o.version,o.execution FROM build_operations o JOIN build_targets t ON o.target_id=t.id WHERE o.project_id=? AND o.id=?"
	case "deployment":
		query = "SELECT o.service_id,t.generation,o.version,o.execution FROM deployment_operations o JOIN deployment_targets t ON o.target_id=t.id WHERE o.project_id=? AND o.id=?"
	default:
		return fmt.Errorf("项目通知来源不受支持")
	}
	var generation, version, execution uint64
	reference := &projectstate.Reference{OperationID: operationID}
	if err := tx.QueryRowContext(ctx, query, id, operationID).Scan(&reference.ServiceID, &generation, &version, &execution); err != nil {
		return err
	}
	reference.Generation, reference.OperationVersion, reference.Execution = strconv.FormatUint(generation, 10), strconv.FormatUint(version, 10), strconv.FormatUint(execution, 10)
	return appendStateEvent(ctx, tx, id, scope, reference)
}
