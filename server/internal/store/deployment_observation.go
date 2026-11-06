// 本文件被动采集原服务，不因外部停止、删除或漂移自动修复。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// DeploymentTargets 返回已结算原目标供只读采集；在途执行由其工作者拥有。
func (store *Store) DeploymentTargets(ctx context.Context) (values []servicedeploy.Target, err error) {
	rows, err := store.db.QueryContext(ctx, `SELECT t.payload FROM deployment_instances i JOIN deployment_operations o ON i.current_operation_id=o.id JOIN deployment_targets t ON o.target_id=t.id WHERE o.state IN ('succeeded','failed','needs_attention') ORDER BY i.project_id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	values = []servicedeploy.Target{}
	for rows.Next() {
		var data []byte
		var value servicedeploy.Target
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := decodeRuntimeJSON(data, &value); err != nil {
			return nil, err
		}
		if err := servicedeploy.ValidatePlan(value.Plan); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// ObserveDeployment 条件更新仍是当前的目标，迟到观测不能覆盖新代次。
func (store *Store) ObserveDeployment(ctx context.Context, target servicedeploy.Target, fact *servicedeploy.Observation, code string, at time.Time) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		if err := lockBuildProject(ctx, tx, target.ProjectID); err != nil {
			return false, err
		}
		instance, err := readDeploymentInstance(ctx, tx, target.ProjectID, true)
		if err != nil || instance == nil {
			return false, err
		}
		if instance.serviceID != target.ServiceID || strconv.FormatUint(instance.generation, 10) != target.Generation {
			return false, nil
		}
		return true, saveDeploymentObservation(ctx, tx, target, fact, code, at)
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
		return err
	}
	data, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE deployment_instances SET observation_json=?,observation_code=?,last_attempt_at=?,ever_ready=ever_ready OR ? WHERE project_id=?", data, code, at, fact.Ready, target.ProjectID)
	return err
}
