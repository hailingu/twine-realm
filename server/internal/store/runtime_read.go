// 本文件在元数据库一致快照内恢复运行目标与操作，不查询 Docker 推测事实。
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// runtimeInstanceRow 保留当前指针和单调数据身份标记，历史目标另有不可变行。
type runtimeInstanceRow struct {
	projectID, componentID, installationID, resourceID, operationID string
	generation, sequence                                            uint64
	resource                                                        mysqlruntime.Resource
	volumeConfirmed                                                 bool
	serverUUID                                                      string
	observation                                                     []byte
	observationCode                                                 string
	lastAttempt                                                     sql.NullTime
}

// runtimeOperationRow 在公开操作之外携带领取信息，不把租约作为前端可写字段。
type runtimeOperationRow struct {
	value       mysqlruntime.Operation
	execution   uint64
	lease       sql.NullTime
	nextAttempt time.Time
	targetID    string
}

// runtimeRead 用一次可重复读屏障组合目标、步骤与事件版本。
func runtimeRead[T any](ctx context.Context, database *sql.DB, action func(*sql.Tx) (T, error)) (value T, err error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return value, err
	}
	defer func() {
		if rollback := tx.Rollback(); !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	value, err = action(tx)
	if err != nil {
		return value, err
	}
	return value, tx.Commit()
}

// RuntimeReceipt 返回已提交受理结果与原意图摘要，未提交占位不算成功。
func (store *Store) RuntimeReceipt(ctx context.Context, id string) (mysqlruntime.Receipt, [32]byte, error) {
	return readRuntimeReceipt(ctx, store.db, id)
}

// readRuntimeReceipt 只恢复固定响应，不读当前 Docker 或操作状态。
func readRuntimeReceipt(ctx context.Context, database queryer, id string) (mysqlruntime.Receipt, [32]byte, error) {
	var value mysqlruntime.Receipt
	var hash [32]byte
	var data, stored []byte
	err := database.QueryRowContext(ctx, "SELECT intent_hash,response FROM runtime_receipts WHERE id=?", id).Scan(&stored, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, hash, project.Failure("MUTATION_NOT_FOUND", "尚未找到原运行请求，可按原请求身份重试")
	}
	if err != nil {
		return value, hash, err
	}
	if len(stored) != len(hash) || len(data) == 0 {
		return value, hash, fmt.Errorf("运行回执不满足完整性约束")
	}
	copy(hash[:], stored)
	if err := decodeRuntimeJSON(data, &value); err != nil {
		return value, hash, err
	}
	return value, hash, nil
}

// RuntimeSnapshot 读取同一画布所属的当前运行快照，空状态仍有明确版本零。
func (store *Store) RuntimeSnapshot(ctx context.Context, id string) (mysqlruntime.Snapshot, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (mysqlruntime.Snapshot, error) { return readRuntimeSnapshot(ctx, tx, id) })
}

// readRuntimeSnapshot 保留最后事实并由服务端判定未知或20秒过期。
func readRuntimeSnapshot(ctx context.Context, database queryer, id string) (mysqlruntime.Snapshot, error) {
	value := mysqlruntime.Snapshot{SchemaVersion: 1, ProjectID: id, Generation: "0", Sequence: "0", Freshness: "unknown"}
	instance, err := readRuntimeInstance(ctx, database, id, false)
	if err != nil {
		return value, err
	}
	if instance == nil {
		return value, runtimeProjectExists(ctx, database, id)
	}
	value.Generation, value.Sequence = strconv.FormatUint(instance.generation, 10), strconv.FormatUint(instance.sequence, 10)
	target, err := readRuntimeTarget(ctx, database, id, value.Generation)
	if err != nil {
		return value, err
	}
	if err := matchRuntimeTarget(instance, target); err != nil {
		return value, err
	}
	value.Target = &target
	op, err := readRuntimeOperation(ctx, database, id, instance.operationID)
	if err != nil {
		return value, err
	}
	value.Operation = &op.value
	value.ObservationCode = instance.observationCode
	if instance.lastAttempt.Valid {
		value.LastAttemptAt = mysqlruntime.Timestamp(instance.lastAttempt.Time)
	}
	err = attachRuntimeFact(&value, instance)
	return value, err
}

// attachRuntimeFact 保留最后采样并独立计算新鲜度，不根据操作终态推断健康。
func attachRuntimeFact(value *mysqlruntime.Snapshot, instance *runtimeInstanceRow) error {
	fact, err := previousRuntimeFact(instance)
	if err != nil || fact == nil {
		return err
	}
	value.Observation = fact
	observed, err := time.Parse(time.RFC3339Nano, fact.ObservedAt)
	if err != nil {
		return fmt.Errorf("运行观测时间无效")
	}
	if value.ObservationCode != "" || observed.After(time.Now().Add(5*time.Second)) {
		return nil
	}
	value.Freshness = "fresh"
	if time.Since(observed) > 20*time.Second {
		value.Freshness = "stale"
	}
	return nil
}

// runtimeProjectExists 不把不存在项目当作尚未开始运行。
func runtimeProjectExists(ctx context.Context, database queryer, id string) error {
	var found string
	err := database.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=?", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Failure("PROJECT_NOT_FOUND", "项目不存在")
	}
	return err
}

// readRuntimeInstance 在写入路径显式锁定项目运行行，统一锁顺序。
func readRuntimeInstance(ctx context.Context, database queryer, id string, locked bool) (*runtimeInstanceRow, error) {
	query := `SELECT project_id,component_id,installation_id,resource_id,resource_json,generation,sequence,current_operation_id,volume_confirmed,server_uuid,observation_json,observation_code,last_attempt_at FROM runtime_instances WHERE project_id=?`
	if locked {
		query += " FOR UPDATE"
	}
	var value runtimeInstanceRow
	var resource []byte
	err := database.QueryRowContext(ctx, query, id).Scan(&value.projectID, &value.componentID, &value.installationID, &value.resourceID, &resource, &value.generation, &value.sequence, &value.operationID, &value.volumeConfirmed, &value.serverUUID, &value.observation, &value.observationCode, &value.lastAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := decodeRuntimeJSON(resource, &value.resource); err != nil {
		return nil, err
	}
	if value.resource.ID != value.resourceID || value.resource.ProjectID != id || value.resource.ComponentID != value.componentID || value.resource.InstallationID != value.installationID {
		return nil, fmt.Errorf("运行资源归属记录不一致")
	}
	return &value, nil
}

// readRuntimeTarget 以索引身份核对完整载荷，损坏不能静默变成可执行计划。
func readRuntimeTarget(ctx context.Context, database queryer, id, generation string) (mysqlruntime.Target, error) {
	var value mysqlruntime.Target
	var targetID, desired string
	var data []byte
	err := database.QueryRowContext(ctx, "SELECT id,desired,payload FROM runtime_targets WHERE project_id=? AND generation=?", id, generation).Scan(&targetID, &desired, &data)
	if err != nil {
		return value, err
	}
	if err := decodeRuntimeJSON(data, &value); err != nil {
		return value, err
	}
	if value.ID != targetID || value.ProjectID != id || value.Generation != generation || value.Desired != desired {
		return value, fmt.Errorf("不可变运行目标不满足身份约束")
	}
	return value, nil
}

// RuntimeOperation 读取原操作和步骤，即使已经被新目标替代也不丢弃诊断。
func (store *Store) RuntimeOperation(ctx context.Context, id, operationID string) (mysqlruntime.Operation, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (mysqlruntime.Operation, error) {
		row, err := readRuntimeOperation(ctx, tx, id, operationID)
		return row.value, err
	})
}

// readRuntimeOperation 返回内部领取信息和已排序步骤，所有查询都核对项目归属。
func readRuntimeOperation(ctx context.Context, database queryer, id, operationID string) (value runtimeOperationRow, err error) {
	var generation, version uint64
	var created, updated time.Time
	err = database.QueryRowContext(ctx, `SELECT id,mutation_id,project_id,generation,state,version,attempts,code,message,retry_of,created_at,updated_at,execution,lease_until,next_attempt,target_id FROM runtime_operations WHERE id=? AND project_id=?`, operationID, id).Scan(&value.value.ID, &value.value.MutationID, &value.value.ProjectID, &generation, &value.value.State, &version, &value.value.Attempts, &value.value.Code, &value.value.Message, &value.value.RetryOf, &created, &updated, &value.execution, &value.lease, &value.nextAttempt, &value.targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return value, project.Failure("RUNTIME_OPERATION_NOT_FOUND", "运行操作不存在或不属于此项目")
	}
	if err != nil {
		return value, err
	}
	value.value.Generation, value.value.Version = strconv.FormatUint(generation, 10), strconv.FormatUint(version, 10)
	value.value.CreatedAt, value.value.UpdatedAt = mysqlruntime.Timestamp(created), mysqlruntime.Timestamp(updated)
	value.value.Steps = []mysqlruntime.Step{}
	rows, err := database.QueryContext(ctx, "SELECT name,state,code,external_id,updated_at FROM runtime_steps WHERE operation_id=? ORDER BY FIELD(name,'preflight','credentials','network','volume','container','readiness','stop','stopped')", operationID)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var step mysqlruntime.Step
		var updated time.Time
		if err := rows.Scan(&step.Name, &step.State, &step.Code, &step.ExternalID, &updated); err != nil {
			return value, err
		}
		step.UpdatedAt = mysqlruntime.Timestamp(updated)
		value.value.Steps = append(value.value.Steps, step)
	}
	return value, rows.Err()
}

// RuntimeEvents 每次最多返回100条，nextSequence 对应实际返回的最后一条。
func (store *Store) RuntimeEvents(ctx context.Context, id, sequence string) (mysqlruntime.EventPage, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (mysqlruntime.EventPage, error) { return readRuntimeEvents(ctx, tx, id, sequence) })
}

// readRuntimeEvents 多取一行判断剩余项，不把未知游标当完整历史。
func readRuntimeEvents(ctx context.Context, database queryer, id, sequence string) (value mysqlruntime.EventPage, err error) {
	value = mysqlruntime.EventPage{SchemaVersion: 1, Events: []mysqlruntime.Event{}, NextSequence: sequence}
	if err := runtimeProjectExists(ctx, database, id); err != nil {
		return value, err
	}
	rows, err := database.QueryContext(ctx, "SELECT sequence,operation_id,generation,kind,code,created_at FROM runtime_events WHERE project_id=? AND sequence>? ORDER BY sequence LIMIT 101", id, sequence)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var event mysqlruntime.Event
		var number, generation uint64
		var created time.Time
		if err := rows.Scan(&number, &event.OperationID, &generation, &event.Kind, &event.Code, &created); err != nil {
			return value, err
		}
		if len(value.Events) == 100 {
			value.HasMore = true
			break
		}
		event.Sequence, event.Generation, event.CreatedAt = strconv.FormatUint(number, 10), strconv.FormatUint(generation, 10), mysqlruntime.Timestamp(created)
		value.Events = append(value.Events, event)
		value.NextSequence = event.Sequence
	}
	return value, rows.Err()
}

// decodeRuntimeJSON 拒绝超限、未知结构字段和尾随内容，不解码任意远程材料。
func decodeRuntimeJSON(data []byte, target any) error {
	if len(data) == 0 || len(data) > 65536 {
		return fmt.Errorf("运行元数据载荷大小无效")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("运行元数据结构无效")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("运行元数据存在额外内容")
	}
	return nil
}
