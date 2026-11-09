// 本文件以项目行锁重验计划，目标、操作、事件与原键回执必须一起提交。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// AcceptDeployment 先原键占位，再重验，提交未知时绝不执行下一外部动作。
func (store *Store) AcceptDeployment(ctx context.Context, input servicedeploy.AcceptedInput) (servicedeploy.Receipt, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (servicedeploy.Receipt, error) {
		_, err := tx.ExecContext(ctx, "INSERT INTO deployment_receipts(id,intent_hash,created_at) VALUES(?,?,?)", input.Command.MutationID, input.Hash[:], time.Now().UTC())
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			value, hash, err := readDeploymentReceipt(ctx, tx, input.Command.MutationID)
			if err == nil && hash != input.Hash {
				err = project.Failure("IDEMPOTENCY_CONFLICT", "相同部署请求身份已用于不同内容")
			}
			value.Replayed = err == nil
			return value, err
		}
		if err != nil {
			return servicedeploy.Receipt{}, err
		}
		value, err := acceptDeploymentLocked(ctx, tx, input)
		if err != nil {
			return value, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return value, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE deployment_receipts SET response=? WHERE id=?", data, input.Command.MutationID)
		return value, err
	})
}

// acceptDeploymentLocked 不信任HTTP预检后仍未变化，按同一项目锁序重读各模块。
func acceptDeploymentLocked(ctx context.Context, tx *sql.Tx, input servicedeploy.AcceptedInput) (value servicedeploy.Receipt, err error) {
	p := input.Plan
	if err := lockRevision(ctx, tx, p.ProjectID, input.Command.Preview.BaseRevision); err != nil {
		return value, err
	}
	if err := projectRunPermission(ctx, tx, p.ProjectID); err != nil {
		return value, err
	}
	if err := revalidateDeployment(ctx, tx, input); err != nil {
		return value, err
	}
	instance, err := readDeploymentInstance(ctx, tx, p.ProjectID, true)
	if err != nil {
		return value, err
	}
	if instance == nil {
		if _, err := tx.ExecContext(ctx, "INSERT INTO deployment_instances(project_id,service_id) VALUES(?,?)", p.ProjectID, p.ServiceID); err != nil {
			return value, err
		}
		instance = &deploymentInstance{serviceID: p.ServiceID}
	}
	if instance.serviceID != p.ServiceID {
		return value, project.Failure("DEPLOY_SERVICE_LIMIT", "首个运行组合一个项目只支持一个业务服务")
	}
	if strconv.FormatUint(instance.generation, 10) != input.Command.Preview.BaseGeneration || instance.generation == math.MaxUint64 {
		return value, project.Failure("DEPLOY_GENERATION_CONFLICT", "部署代次已改变，请核实原操作")
	}
	if err := previousDeployment(ctx, tx, p, instance, input.Command.RetryOf); err != nil {
		return value, err
	}
	targetID, err := project.NewID()
	if err != nil {
		return value, err
	}
	operationID, err := project.NewID()
	if err != nil {
		return value, err
	}
	now := mysqlruntime.Timestamp(time.Now())
	target := servicedeploy.Target{ID: targetID, ProjectID: p.ProjectID, ServiceID: p.ServiceID, Generation: strconv.FormatUint(instance.generation+1, 10), Plan: p, CreatedAt: now}
	op := servicedeploy.Operation{ID: operationID, TargetID: targetID, ProjectID: p.ProjectID, ServiceID: p.ServiceID, Version: "1", State: "accepted", Code: "ACCEPTED", Message: "部署目标已持久接受，迁移及应用尚未确认就绪", Steps: []servicedeploy.Step{}, CreatedAt: now, UpdatedAt: now}
	if err := insertDeployment(ctx, tx, target, op); err != nil {
		return value, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE deployment_instances SET generation=?,current_operation_id=?,observation_code='DEPLOY_PENDING' WHERE project_id=?", target.Generation, op.ID, p.ProjectID)
	return servicedeploy.Receipt{SchemaVersion: 1, MutationID: input.Command.MutationID, OperationID: op.ID, Target: target}, err
}

// revalidateDeployment 在同一事务检查源码、候选、连线、运行代次与数据库UUID。
func revalidateDeployment(ctx context.Context, tx *sql.Tx, input servicedeploy.AcceptedInput) error {
	p := input.Plan
	graph := project.GraphSnapshot{}
	var err error
	graph.Project, err = readProject(ctx, tx, p.ProjectID)
	if err != nil {
		return err
	}
	graph.Graph, err = readGraphState(ctx, tx, p.ProjectID)
	if err != nil {
		return err
	}
	build, err := readBuildSnapshot(ctx, tx, p.ProjectID, p.ServiceID)
	if err != nil {
		return err
	}
	runtime, err := readRuntimeSnapshot(ctx, tx, p.ProjectID)
	if err != nil {
		return err
	}
	if runtime.Target == nil || runtime.Operation == nil || runtime.Operation.State != "succeeded" || runtime.Target.Desired != "running" || runtime.Observation == nil || !runtime.Observation.Ready || runtime.Freshness != "fresh" || runtime.Observation.ServerUUID != p.Database.ServerUUID || runtime.Generation != p.Database.Generation {
		return project.Failure("DEPLOY_DATABASE_NOT_READY", "MySQL必须是已完成且新鲜认证就绪的原运行目标")
	}
	digest, err := mysqlruntime.Fingerprint(runtime.Target.Resource)
	if err != nil {
		return err
	}
	if digest != p.Database.ResourceDigest || runtime.Target.Resource.ID != p.Database.ResourceID || build.Target == nil || build.Target.InstallationID != p.Environment.BuildInstallationID {
		return project.Failure("DEPLOY_DATABASE_CHANGED", "运行安装、固定资源或构建安装已改变")
	}
	current, err := servicedeploy.MakePlan(graph, build, input.Command.Preview, p.Database, p.Environment)
	if err != nil {
		return err
	}
	if current.Digest != input.Command.PlanDigest || current.Digest != p.Digest {
		return project.Failure("DEPLOY_PLAN_CHANGED", "部署预览已改变，请重新审阅并确认")
	}
	return nil
}

// previousDeployment 首版只恢复原物理计划，禁止借重试自动替换已创建服务或迁移历史。
func previousDeployment(ctx context.Context, tx *sql.Tx, p servicedeploy.Plan, instance *deploymentInstance, retryOf string) error {
	if instance.operationID == "" {
		if retryOf != "" {
			return project.Failure("DEPLOY_RETRY_CONFLICT", "没有可关联的原部署")
		}
		return nil
	}
	row, err := readDeploymentOperation(ctx, tx, p.ProjectID, p.ServiceID, instance.operationID)
	if err != nil {
		return err
	}
	if !servicedeploy.Terminal(row.value.State) {
		return project.Failure("DEPLOY_BUSY", "原部署仍在执行，请先核实原操作")
	}
	if retryOf != instance.operationID {
		return project.Failure("DEPLOY_RETRY_CONFLICT", "恢复须显式关联当前原部署")
	}
	target, err := readDeploymentTarget(ctx, tx, row.value.TargetID)
	if err != nil {
		return err
	}
	if target.Plan.RuntimeDigest != p.RuntimeDigest {
		return project.Failure("DEPLOY_REPLACEMENT_UNSUPPORTED", "首版仅支持原候选与原配置的核实恢复，不自动替换已登记物理计划")
	}
	return nil
}

// insertDeployment 事件与受理载荷不能先后独立落库。
func insertDeployment(ctx context.Context, tx *sql.Tx, target servicedeploy.Target, op servicedeploy.Operation) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO deployment_targets(id,project_id,service_id,generation,payload) VALUES(?,?,?,?,?)", target.ID, target.ProjectID, target.ServiceID, target.Generation, data); err != nil {
		return err
	}
	data, err = json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO deployment_operations(id,project_id,service_id,target_id,state,payload,created_at) VALUES(?,?,?,?,?,?,?)", op.ID, op.ProjectID, op.ServiceID, op.TargetID, op.State, data, time.Now().UTC()); err != nil {
		return err
	}
	return appendDeploymentEvent(ctx, tx, op, "accepted", data)
}
