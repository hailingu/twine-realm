// 本文件在同一数据库快照读取构建追溯与原请求，不根据进程消失推断成功。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// buildInstance 保存当前指针与最后成功候选的历史指针，二者不得混为一谈。
type buildInstance struct {
	generation               uint64
	operationID, candidateID string
}

// buildOperationRow 除公开载荷外保存执行围栏；读取时交叉核实重复字段。
type buildOperationRow struct {
	value     servicebuild.Operation
	execution uint64
	lease     sql.NullTime
}

// BuildReceipt 只返回已提交原意图；查不到不表示请求必定没有在途提交。
func (store *Store) BuildReceipt(ctx context.Context, id string) (servicebuild.Receipt, [32]byte, error) {
	return readBuildReceipt(ctx, store.db, id)
}

// readBuildReceipt 对完整响应与意图摘要同时校验，不以占位当作受理。
func readBuildReceipt(ctx context.Context, database queryer, id string) (value servicebuild.Receipt, hash [32]byte, err error) {
	var data, stored []byte
	err = database.QueryRowContext(ctx, "SELECT intent_hash,response FROM build_receipts WHERE id=?", id).Scan(&stored, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, hash, project.Failure("MUTATION_NOT_FOUND", "尚未找到原构建请求，请保持原键核实")
	}
	if err != nil {
		return value, hash, err
	}
	if len(stored) != len(hash) {
		return value, hash, fmt.Errorf("构建回执摘要不完整")
	}
	copy(hash[:], stored)
	err = decodeRuntimeJSON(data, &value)
	if err == nil && value.MutationID != id {
		err = fmt.Errorf("构建回执归属不一致")
	}
	return value, hash, err
}

// readBuildInstance 在项目行锁之后读取或锁定服务构建指针，锁序始终相同。
func readBuildInstance(ctx context.Context, database queryer, id, serviceID string, locked bool) (*buildInstance, error) {
	query := "SELECT generation,current_operation_id,candidate_operation_id FROM build_instances WHERE project_id=? AND service_id=?"
	if locked {
		query += " FOR UPDATE"
	}
	var value buildInstance
	err := database.QueryRowContext(ctx, query, id, serviceID).Scan(&value.generation, &value.operationID, &value.candidateID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &value, err
}

// readBuildTarget 核对索引与JSON的不可变身份及实际输入摘要。
func readBuildTarget(ctx context.Context, database queryer, id string) (value servicebuild.Target, err error) {
	var projectID, serviceID string
	var generation uint64
	var data []byte
	err = database.QueryRowContext(ctx, "SELECT project_id,service_id,generation,payload FROM build_targets WHERE id=?", id).Scan(&projectID, &serviceID, &generation, &data)
	if err != nil {
		return value, err
	}
	if err := decodeRuntimeJSON(data, &value); err != nil {
		return value, err
	}
	digest, err := servicebuild.InputFingerprint(value.Source, value.Profile, value.Environment, value.Policy, value.InstallationID)
	if err != nil {
		return value, err
	}
	if value.ID != id || value.ProjectID != projectID || value.ServiceID != serviceID || value.Generation != strconv.FormatUint(generation, 10) || value.InputDigest != digest {
		return value, fmt.Errorf("构建目标归属或摘要不一致")
	}
	return value, nil
}

// readBuildOperation 将历史载荷与执行列交叉核实，损坏记录不能成为候选。
func readBuildOperation(ctx context.Context, database queryer, id, serviceID, operationID string) (value buildOperationRow, err error) {
	var targetID, state string
	var version uint64
	var data []byte
	err = database.QueryRowContext(ctx, "SELECT target_id,state,version,execution,lease_until,payload FROM build_operations WHERE id=? AND project_id=? AND service_id=?", operationID, id, serviceID).Scan(&targetID, &state, &version, &value.execution, &value.lease, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, project.Failure("BUILD_OPERATION_NOT_FOUND", "构建操作不存在或不属于此服务")
	}
	if err != nil {
		return value, err
	}
	if err := decodeRuntimeJSON(data, &value.value); err != nil {
		return value, err
	}
	op := value.value
	if op.ID != operationID || op.ProjectID != id || op.ServiceID != serviceID || op.TargetID != targetID || op.State != state || op.Version != strconv.FormatUint(version, 10) {
		return value, fmt.Errorf("构建操作归属或版本不一致")
	}
	return value, nil
}

// BuildOperation 返回完整历史状态，不要求它仍是当前操作。
func (store *Store) BuildOperation(ctx context.Context, id, serviceID, operationID string) (servicebuild.Operation, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (servicebuild.Operation, error) {
		row, err := readBuildOperation(ctx, tx, id, serviceID, operationID)
		return row.value, err
	})
}

// BuildSnapshot 在同一读事务组合当前源码、目标、操作与最后成功候选。
func (store *Store) BuildSnapshot(ctx context.Context, id, serviceID string) (servicebuild.Snapshot, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (servicebuild.Snapshot, error) { return readBuildSnapshot(ctx, tx, id, serviceID) })
}

// readBuildSnapshot 未受理服务仍明确返回零代次，而不是伪造构建结果。
func readBuildSnapshot(ctx context.Context, database queryer, id, serviceID string) (value servicebuild.Snapshot, err error) {
	value = servicebuild.Snapshot{SchemaVersion: 1, ProjectID: id, ServiceID: serviceID, Generation: "0", CandidateCode: "NO_CANDIDATE"}
	value.CurrentSource, err = readSource(ctx, database, id, serviceID)
	if err != nil {
		return value, err
	}
	instance, err := readBuildInstance(ctx, database, id, serviceID, false)
	if err != nil || instance == nil {
		return value, err
	}
	value.Generation = strconv.FormatUint(instance.generation, 10)
	op, err := readBuildOperation(ctx, database, id, serviceID, instance.operationID)
	if err != nil {
		return value, err
	}
	target, err := readBuildTarget(ctx, database, op.value.TargetID)
	if err != nil {
		return value, err
	}
	if target.Generation != value.Generation {
		return value, fmt.Errorf("构建快照指针不一致")
	}
	value.Target, value.Operation = &target, &op.value
	if instance.candidateID != "" {
		success, err := readBuildOperation(ctx, database, id, serviceID, instance.candidateID)
		if err != nil {
			return value, err
		}
		if success.value.State != "succeeded" || success.value.Candidate == nil {
			return value, fmt.Errorf("构建候选指针未指向成功操作")
		}
		value.Candidate, value.CandidateCode = success.value.Candidate, "HISTORICAL_CANDIDATE"
	}
	return value, nil
}
