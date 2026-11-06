// 本文件在一致元数据快照读取业务部署，不用容器存在代替受理或成功证据。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentInstance 保存单服务切片的当前指针与最后采集，不暴露执行租约。
type deploymentInstance struct {
	serviceID, operationID string
	generation, sequence   uint64
	everReady              bool
	observation            []byte
	code                   string
	lastAttempt            sql.NullTime
}

// deploymentOperationRow 交叉核实公开JSON与索引字段，损坏记录不能获得执行许可。
type deploymentOperationRow struct {
	value     servicedeploy.Operation
	execution uint64
	lease     sql.NullTime
}

// readDeploymentInstance 沿用项目先锁的顺序，一个画布首版只接纳一个业务服务。
func readDeploymentInstance(ctx context.Context, database queryer, id string, locked bool) (*deploymentInstance, error) {
	query := "SELECT service_id,generation,sequence,current_operation_id,ever_ready,observation_json,observation_code,last_attempt_at FROM deployment_instances WHERE project_id=?"
	if locked {
		query += " FOR UPDATE"
	}
	var value deploymentInstance
	err := database.QueryRowContext(ctx, query, id).Scan(&value.serviceID, &value.generation, &value.sequence, &value.operationID, &value.everReady, &value.observation, &value.code, &value.lastAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &value, err
}

// readDeploymentTarget 同时校验完整计划与行级归属。
func readDeploymentTarget(ctx context.Context, database queryer, id string) (value servicedeploy.Target, err error) {
	var projectID, serviceID string
	var generation uint64
	var data []byte
	err = database.QueryRowContext(ctx, "SELECT project_id,service_id,generation,payload FROM deployment_targets WHERE id=?", id).Scan(&projectID, &serviceID, &generation, &data)
	if err != nil {
		return value, err
	}
	if err := decodeRuntimeJSON(data, &value); err != nil {
		return value, err
	}
	if value.ID != id || value.ProjectID != projectID || value.ServiceID != serviceID || value.Generation != strconv.FormatUint(generation, 10) || value.Plan.ProjectID != projectID || value.Plan.ServiceID != serviceID {
		return value, fmt.Errorf("部署目标归属不一致")
	}
	return value, servicedeploy.ValidatePlan(value.Plan)
}

// readDeploymentOperation 不把租约到期或未知外部结果当作未开始。
func readDeploymentOperation(ctx context.Context, database queryer, id, serviceID, operationID string) (row deploymentOperationRow, err error) {
	var target, state string
	var version uint64
	var data []byte
	err = database.QueryRowContext(ctx, "SELECT target_id,state,version,execution,lease_until,payload FROM deployment_operations WHERE id=? AND project_id=? AND service_id=?", operationID, id, serviceID).Scan(&target, &state, &version, &row.execution, &row.lease, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return row, project.Failure("DEPLOY_OPERATION_NOT_FOUND", "部署操作不存在或不属于此服务")
	}
	if err != nil {
		return row, err
	}
	if err := decodeRuntimeJSON(data, &row.value); err != nil {
		return row, err
	}
	op := row.value
	if op.ID != operationID || op.ProjectID != id || op.ServiceID != serviceID || op.TargetID != target || op.State != state || op.Version != strconv.FormatUint(version, 10) {
		return row, fmt.Errorf("部署操作身份或版本不一致")
	}
	return row, nil
}

// DeploymentReceipt 只返回原键已提交响应，不根据现在配置重新受理。
func (store *Store) DeploymentReceipt(ctx context.Context, id string) (servicedeploy.Receipt, [32]byte, error) {
	return readDeploymentReceipt(ctx, store.db, id)
}

// readDeploymentReceipt 同时读取意图摘要与完整响应，空占位不构成接受。
func readDeploymentReceipt(ctx context.Context, database queryer, id string) (value servicedeploy.Receipt, hash [32]byte, err error) {
	var stored, data []byte
	err = database.QueryRowContext(ctx, "SELECT intent_hash,response FROM deployment_receipts WHERE id=?", id).Scan(&stored, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, hash, project.Failure("MUTATION_NOT_FOUND", "尚未找到原部署请求，请保持原键核实")
	}
	if err != nil {
		return value, hash, err
	}
	if len(stored) != 32 || len(data) == 0 {
		return value, hash, fmt.Errorf("部署回执不完整")
	}
	copy(hash[:], stored)
	err = decodeRuntimeJSON(data, &value)
	if err == nil && value.MutationID != id {
		err = fmt.Errorf("部署回执归属不一致")
	}
	return value, hash, err
}

// DeploymentOperation 提供原操作历史，当前目标改变不抹除原结果。
func (store *Store) DeploymentOperation(ctx context.Context, id, serviceID, operationID string) (servicedeploy.Operation, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (servicedeploy.Operation, error) {
		row, err := readDeploymentOperation(ctx, tx, id, serviceID, operationID)
		return row.value, err
	})
}

// DeploymentSnapshot 用一次读屏障组合当前目标、操作和独立新鲜度。
func (store *Store) DeploymentSnapshot(ctx context.Context, id, serviceID string) (servicedeploy.Snapshot, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (servicedeploy.Snapshot, error) {
		return readDeploymentSnapshot(ctx, tx, id, serviceID)
	})
}

// readDeploymentSnapshot 未受理服务明确显示零代次；最后成功不等于现在仍可达。
func readDeploymentSnapshot(ctx context.Context, database queryer, id, serviceID string) (value servicedeploy.Snapshot, err error) {
	value = servicedeploy.Snapshot{SchemaVersion: 1, ProjectID: id, ServiceID: serviceID, Generation: "0", Sequence: "0", Freshness: "unknown"}
	if _, err := readSource(ctx, database, id, serviceID); err != nil {
		return value, err
	}
	instance, err := readDeploymentInstance(ctx, database, id, false)
	if err != nil || instance == nil || instance.serviceID != serviceID {
		return value, err
	}
	row, err := readDeploymentOperation(ctx, database, id, serviceID, instance.operationID)
	if err != nil {
		return value, err
	}
	target, err := readDeploymentTarget(ctx, database, row.value.TargetID)
	if err != nil {
		return value, err
	}
	value.Target, value.Operation = &target, &row.value
	value.Generation, value.Sequence = strconv.FormatUint(instance.generation, 10), strconv.FormatUint(instance.sequence, 10)
	if target.Generation != value.Generation {
		return value, fmt.Errorf("部署目标指针不一致")
	}
	return deploymentObservation(value, instance)
}

// deploymentObservation 单独恢复最后事实和新鲜度，不访问外部资源或刷新旧ready时间。
func deploymentObservation(value servicedeploy.Snapshot, instance *deploymentInstance) (servicedeploy.Snapshot, error) {
	if len(instance.observation) > 0 {
		value.Observation = &servicedeploy.Observation{}
		if err := decodeRuntimeJSON(instance.observation, value.Observation); err != nil {
			return value, err
		}
	}
	value.ObservationCode = instance.code
	if instance.lastAttempt.Valid {
		value.LastAttemptAt = instance.lastAttempt.Time.UTC().Format(time.RFC3339Nano)
		if instance.code == "" && value.Observation != nil {
			value.Freshness = "fresh"
			if time.Since(instance.lastAttempt.Time) > 20*time.Second {
				value.Freshness = "stale"
			}
		}
	}
	return value, nil
}
