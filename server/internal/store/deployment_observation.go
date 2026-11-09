// 本文件被动采集原服务，不因外部停止、删除或漂移自动修复。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/project"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// DeploymentTargets 返回已结算原目标供只读采集；在途执行由其工作者拥有。
func (store *Store) DeploymentTargets(ctx context.Context) (values []servicedeploy.Target, err error) {
	rows, err := store.db.QueryContext(ctx, `SELECT t.payload,COALESCE(p.observation_epoch,0) FROM deployment_instances i JOIN deployment_operations o ON i.current_operation_id=o.id JOIN deployment_targets t ON o.target_id=t.id LEFT JOIN projectrun_instances p ON p.project_id=i.project_id WHERE o.state IN ('succeeded','failed','needs_attention') ORDER BY i.project_id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	values = []servicedeploy.Target{}
	for rows.Next() {
		var data []byte
		var epoch uint64
		var value servicedeploy.Target
		if err := rows.Scan(&data, &epoch); err != nil {
			return nil, err
		}
		if err := decodeRuntimeJSON(data, &value); err != nil {
			return nil, err
		}
		if err := servicedeploy.ValidatePlan(value.Plan); err != nil {
			return nil, err
		}
		value.ObservationEpoch = strconv.FormatUint(epoch, 10)
		values = append(values, value)
	}
	return values, rows.Err()
}

// ObserveDeployment 条件更新仍是当前的目标，迟到观测不能覆盖新代次。
func (store *Store) ObserveDeployment(ctx context.Context, target servicedeploy.Target, fact *servicedeploy.Observation, code string, at time.Time) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		return observeDeploymentLocked(ctx, tx, target, fact, code, at)
	})
	return err
}

// saveDeploymentObservation 采集失败保留最后事实和独立错误，不把旧ready当作新鲜健康。
func saveDeploymentObservation(ctx context.Context, tx *sql.Tx, target servicedeploy.Target, fact *servicedeploy.Observation, code string, at time.Time) error {
	instance, err := readDeploymentInstance(ctx, tx, target.ProjectID, false)
	if err != nil {
		return err
	}
	if instance == nil {
		return fmt.Errorf("观测缺少原部署实例")
	}
	current, err := currentDeploymentFact(instance, target, fact, at)
	if err != nil || !current {
		return err
	}
	if fact == nil {
		if code == "" {
			code = "DEPLOY_OBSERVATION_UNKNOWN"
		}
		_, err := tx.ExecContext(ctx, "UPDATE deployment_instances SET observation_code=?,last_attempt_at=? WHERE project_id=?", code, at, target.ProjectID)
		if err != nil {
			return err
		}
		return appendDomainStateEvent(ctx, tx, target.ProjectID, "deployment", instance.operationID)
	}
	data, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE deployment_instances SET observation_json=?,observation_code=?,last_attempt_at=?,ever_ready=ever_ready OR ? WHERE project_id=?", data, code, at, fact.Ready, target.ProjectID)
	if err != nil {
		return err
	}
	return appendDomainStateEvent(ctx, tx, target.ProjectID, "deployment", instance.operationID)
}

// observeDeploymentLocked 在持有项目事务后完成单一状态转换。
func observeDeploymentLocked(ctx context.Context, tx *sql.Tx, target servicedeploy.Target, fact *servicedeploy.Observation, code string, at time.Time) (bool, error) {
	if err := lockBuildProject(ctx, tx, target.ProjectID); err != nil {
		return false, err
	}
	if err := projectRunPermission(ctx, tx, target.ProjectID); err != nil {
		var blocked *project.Error
		if errors.As(err, &blocked) && blocked.Code == "PROJECT_RUN_BUSY" {
			return false, nil
		}
		return false, err
	}
	epoch, err := projectRunEpoch(ctx, tx, target.ProjectID)
	if err != nil {
		return false, err
	}
	if target.ObservationEpoch != epoch {
		return false, nil
	}
	instance, err := readDeploymentInstance(ctx, tx, target.ProjectID, true)
	if err != nil || instance == nil {
		return false, err
	}
	if instance.serviceID != target.ServiceID || strconv.FormatUint(instance.generation, 10) != target.Generation {
		return false, nil
	}
	return true, saveDeploymentObservation(ctx, tx, target, fact, code, at)
}
