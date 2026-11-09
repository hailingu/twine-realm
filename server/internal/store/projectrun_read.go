// 本文件在同一元数据快照读取父目标和原键回执。
package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// ProjectRunSnapshot 读取一致的父操作与最后事实。
func (store *Store) ProjectRunSnapshot(ctx context.Context, id string) (projectrun.Snapshot, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (projectrun.Snapshot, error) { return readProjectRunSnapshot(ctx, tx, id) })
}

// readProjectRunSnapshot 允许尚未受理父目标的已存在项目。
func readProjectRunSnapshot(ctx context.Context, db queryer, id string) (value projectrun.Snapshot, err error) {
	value = projectrun.Snapshot{SchemaVersion: 1, ProjectID: id, Generation: "0"}
	if err = runtimeProjectExists(ctx, db, id); err != nil {
		return
	}
	var generation uint64
	var target, operation, facts []byte
	err = db.QueryRowContext(ctx, `SELECT i.generation,o.target_json,o.payload,i.facts FROM projectrun_instances i JOIN projectrun_operations o ON o.id=i.current_operation_id WHERE i.project_id=?`, id).Scan(&generation, &target, &operation, &facts)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	value.Generation = strconv.FormatUint(generation, 10)
	value.Target = &projectrun.Target{}
	value.Operation = &projectrun.Operation{}
	if err = decodeRuntimeJSON(target, value.Target); err != nil {
		return
	}
	if err = decodeRuntimeJSON(operation, value.Operation); err != nil {
		return
	}
	if len(facts) > 0 {
		value.Facts = &projectrun.Facts{}
		err = decodeRuntimeJSON(facts, value.Facts)
	}
	return
}

// ProjectRunReceipt 返回请求原键的持久结果。
func (store *Store) ProjectRunReceipt(ctx context.Context, id string) (projectrun.Receipt, [32]byte, error) {
	return readProjectRunReceipt(ctx, store.db, id)
}

// readProjectRunReceipt 不把占位或损坏回执解释为成功。
func readProjectRunReceipt(ctx context.Context, db queryer, id string) (value projectrun.Receipt, hash [32]byte, err error) {
	var data, stored []byte
	err = db.QueryRowContext(ctx, "SELECT response,intent_hash FROM projectrun_receipts WHERE id=?", id).Scan(&data, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		err = project.Failure("MUTATION_NOT_FOUND", "尚未找到项目原请求")
		return
	}
	if err != nil {
		return
	}
	if len(stored) != 32 || len(data) == 0 {
		err = project.Failure("RESULT_UNKNOWN", "项目原请求回执尚未确认")
		return
	}
	copy(hash[:], stored)
	err = decodeRuntimeJSON(data, &value)
	return
}

// ProjectStateV6 将生命周期与旧状态放在同一RR事务内。
func (store *Store) ProjectStateV6(ctx context.Context, id string) (projectstate.SnapshotV6, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (value projectstate.SnapshotV6, err error) {
		value.SchemaVersion = 1
		value.State, err = readProjectState(ctx, tx, id)
		if err != nil {
			return
		}
		value.Lifecycle, err = readProjectRunSnapshot(ctx, tx, id)
		return
	})
}
