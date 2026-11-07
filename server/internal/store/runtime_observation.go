// 本文件独立保存运行事实、新鲜度与数据身份，不以网页连接或操作终态代替观测。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
)

// RuntimeInstances 每批最多16项，调用者轮转游标，不无界加载本机全部项目。
func (store *Store) RuntimeInstances(ctx context.Context, after string) ([]mysqlruntime.Instance, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) ([]mysqlruntime.Instance, error) {
		ids, err := runtimeInstanceIDs(ctx, tx, after)
		if err != nil {
			return nil, err
		}
		values := []mysqlruntime.Instance{}
		for _, id := range ids {
			instance, err := readObservationInstance(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			values = append(values, instance)
		}
		return values, nil
	})
}

// readObservationInstance 组合同一快照的固定目标与单调数据证据，不把损坏指针交给采集器。
func readObservationInstance(ctx context.Context, database queryer, id string) (mysqlruntime.Instance, error) {
	instance, err := readRuntimeInstance(ctx, database, id, false)
	if err != nil {
		return mysqlruntime.Instance{}, err
	}
	if instance == nil {
		return mysqlruntime.Instance{}, fmt.Errorf("运行实例不满足一致快照")
	}
	target, err := readRuntimeTarget(ctx, database, id, strconv.FormatUint(instance.generation, 10))
	if err != nil {
		return mysqlruntime.Instance{}, err
	}
	if err := matchRuntimeTarget(instance, target); err != nil {
		return mysqlruntime.Instance{}, err
	}
	var state string
	if err := database.QueryRowContext(ctx, "SELECT state FROM runtime_operations WHERE id=?", instance.operationID).Scan(&state); err != nil {
		return mysqlruntime.Instance{}, err
	}
	return mysqlruntime.Instance{Target: target, OperationState: state, VolumeConfirmed: instance.volumeConfirmed, ServerUUID: instance.serverUUID}, nil
}

// runtimeInstanceIDs 先关闭行集，再在同一事务读取各实例，避免连接上的重入查询。
func runtimeInstanceIDs(ctx context.Context, database queryer, after string) (ids []string, err error) {
	rows, err := database.QueryContext(ctx, "SELECT project_id FROM runtime_instances WHERE project_id>? ORDER BY project_id LIMIT 16", after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ObserveRuntime 只更新仍匹配的当前目标，迟到的旧目标采集结果直接丢弃。
func (store *Store) ObserveRuntime(ctx context.Context, target mysqlruntime.Target, fact *mysqlruntime.Observation, code string, now time.Time) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		instance, err := readRuntimeInstance(ctx, tx, target.ProjectID, true)
		if err != nil || instance == nil {
			return false, err
		}
		if strconv.FormatUint(instance.generation, 10) != target.Generation {
			return false, nil
		}
		if err := matchRuntimeTarget(instance, target); err != nil {
			return false, err
		}
		_, err = recordRuntimeObservation(ctx, tx, instance, fact, code, now)
		return true, err
	})
	return err
}

// recordRuntimeObservation 保留原数据身份和较新事实；失败只标未知，不抹掉最后观测。
func recordRuntimeObservation(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, fact *mysqlruntime.Observation, code string, now time.Time) (bool, error) {
	if len(code) > 80 {
		return false, fmt.Errorf("观测诊断超过边界")
	}
	if instance.lastAttempt.Valid && instance.lastAttempt.Time.After(now) {
		return false, nil
	}
	previous, err := previousRuntimeFact(instance)
	if err != nil {
		return false, err
	}
	change, err := prepareRuntimeFact(instance, previous, fact, code, now)
	if err != nil || change.ignored {
		return false, err
	}
	changed := observationChanged(previous, change.fact, instance.observationCode, change.code)
	if err := persistRuntimeObservation(ctx, tx, instance, change, now); err != nil {
		return false, err
	}
	if changed {
		if err := appendRuntimeEvent(ctx, tx, instance.projectID, instance.operationID, "observed", change.code, now); err != nil {
			return false, err
		}
	} else if err := appendDomainStateEvent(ctx, tx, instance.projectID, "runtime", instance.operationID); err != nil {
		return false, err
	}
	return change.conflict, nil
}

// runtimeObservationChange 描述一次已经验证的采样决定，未知与迟到不冒充新事实。
type runtimeObservationChange struct {
	fact              *mysqlruntime.Observation
	code              string
	conflict, ignored bool
}

// previousRuntimeFact 只解码已持久化的最后事实，未采样时返回空而不是伪造停止。
func previousRuntimeFact(instance *runtimeInstanceRow) (*mysqlruntime.Observation, error) {
	if len(instance.observation) == 0 {
		return nil, nil
	}
	var value mysqlruntime.Observation
	if err := decodeRuntimeJSON(instance.observation, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

// prepareRuntimeFact 先验证时间和身份，再决定是否写入；不修改调用者的采样对象。
func prepareRuntimeFact(instance *runtimeInstanceRow, previous, fact *mysqlruntime.Observation, code string, now time.Time) (runtimeObservationChange, error) {
	change := runtimeObservationChange{code: code}
	if fact == nil {
		return change, nil
	}
	copy := *fact
	change.fact = &copy
	if err := validateRuntimeObservation(copy, now); err != nil {
		return change, err
	}
	older, err := olderRuntimeFact(previous, &copy)
	if err != nil || older {
		change.ignored = older
		return change, err
	}
	if instance.serverUUID != "" && copy.ServerUUID != "" && instance.serverUUID != copy.ServerUUID {
		change.conflict, change.code = true, "RUNTIME_DATABASE_IDENTITY_CONFLICT"
		copy.State, copy.Ready = "drifted", false
	}
	return change, nil
}

// persistRuntimeObservation 未知仅更新诊断和尝试时刻，完整事实才推进数据证据。
func persistRuntimeObservation(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, change runtimeObservationChange, now time.Time) error {
	if change.fact == nil {
		_, err := tx.ExecContext(ctx, "UPDATE runtime_instances SET observation_code=?,last_attempt_at=? WHERE project_id=?", change.code, now, instance.projectID)
		return err
	}
	return saveRuntimeFact(ctx, tx, instance, *change.fact, change.code, now, change.conflict)
}

// olderRuntimeFact 按实际时间比较小数精度不同的RFC3339，不让迟到旧采样覆盖新事实和诊断。
func olderRuntimeFact(previous, current *mysqlruntime.Observation) (bool, error) {
	if previous == nil {
		return false, nil
	}
	oldTime, oldErr := time.Parse(time.RFC3339Nano, previous.ObservedAt)
	newTime, newErr := time.Parse(time.RFC3339Nano, current.ObservedAt)
	if oldErr != nil || newErr != nil {
		return false, fmt.Errorf("运行观测时间记录无效")
	}
	return oldTime.After(newTime), nil
}

// saveRuntimeFact 单调保存卷已出现标记，原 server_uuid 从不被新观测覆盖。
func saveRuntimeFact(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, fact mysqlruntime.Observation, code string, now time.Time, conflict bool) error {
	data, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	serverUUID := instance.serverUUID
	if serverUUID == "" && !conflict && fact.ServerUUID != "" {
		serverUUID = fact.ServerUUID
	}
	_, err = tx.ExecContext(ctx, "UPDATE runtime_instances SET observation_json=?,observation_code=?,last_attempt_at=?,volume_confirmed=(volume_confirmed OR ?),server_uuid=? WHERE project_id=?", data, code, now, fact.VolumeFound, serverUUID, instance.projectID)
	return err
}

// validateRuntimeObservation 阻止矛盾或未来时间的假事实进入成功判断。
func validateRuntimeObservation(fact mysqlruntime.Observation, now time.Time) error {
	observed, err := time.Parse(time.RFC3339Nano, fact.ObservedAt)
	if err != nil || observed.After(now.Add(5*time.Second)) {
		return fmt.Errorf("运行观测时间无效")
	}
	if fact.Ready && (!fact.Running || !fact.VolumeFound || fact.ServerUUID == "") {
		return fmt.Errorf("运行就绪观测不完整")
	}
	switch fact.State {
	case "not_created", "running", "stopped", "drifted":
	default:
		return fmt.Errorf("运行观测状态无效")
	}
	if len(fact.ContainerID) > 128 || len(fact.ServerUUID) > 36 {
		return fmt.Errorf("运行观测身份超出边界")
	}
	return nil
}

// observationChanged 忽略采集时间变化，避免每次正常轮询都膨胀事件历史。
func observationChanged(previous, current *mysqlruntime.Observation, oldCode, newCode string) bool {
	if oldCode != newCode {
		return true
	}
	if current == nil {
		return false
	}
	if previous == nil {
		return true
	}
	left, right := *previous, *current
	left.ObservedAt, right.ObservedAt = "", ""
	return left != right
}
