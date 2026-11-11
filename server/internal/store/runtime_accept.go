// 本文件只在同一个元数据库受理不可变目标、操作、步骤、事件与原键回执。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// runtimeWrite 明确同库提交屏障，提交未知不得继续发出下一外部步骤。
func runtimeWrite[T any](ctx context.Context, database *sql.DB, action func(*sql.Tx) (T, error)) (value T, err error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
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
	if err := tx.Commit(); err != nil {
		return value, &project.Error{Code: "RESULT_UNKNOWN", Message: "运行写入结果尚未确认，请核实原请求与资源", Cause: err}
	}
	return value, nil
}

// BindRuntime 拒绝以新的文件根或 Docker daemon 静默接管已有运行记录。
func (store *Store) BindRuntime(ctx context.Context, id, daemonID string) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO runtime_installations(singleton,installation_id,daemon_id) VALUES(1,?,?) ON DUPLICATE KEY UPDATE singleton=singleton", id, daemonID); err != nil {
			return false, err
		}
		var existing, daemon string
		if err := tx.QueryRowContext(ctx, "SELECT installation_id,daemon_id FROM runtime_installations WHERE singleton=1 FOR UPDATE").Scan(&existing, &daemon); err != nil {
			return false, err
		}
		if existing != id || daemon != daemonID {
			return false, project.Failure("RUNTIME_INSTALLATION_CONFLICT", "元数据库已有不同运行安装身份，需恢复原配置后核实")
		}
		return true, nil
	})
	return err
}

// AcceptRuntime 回执占位、画布锁、目标与操作要么一起提交，要么一起不生效。
func (store *Store) AcceptRuntime(ctx context.Context, input mysqlruntime.AcceptedInput) (mysqlruntime.Receipt, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (mysqlruntime.Receipt, error) {
		return acceptRuntimeRequest(ctx, tx, input)
	})
}

// replayRuntime 并发同键等待唯一行后返回原响应，不重新检查变化中的声明。
func replayRuntime(ctx context.Context, tx *sql.Tx, id string, hash [32]byte) (mysqlruntime.Receipt, error) {
	value, stored, err := readRuntimeReceipt(ctx, tx, id)
	if err != nil {
		return value, err
	}
	if stored != hash {
		return mysqlruntime.Receipt{}, project.Failure("IDEMPOTENCY_CONFLICT", "相同运行请求身份已用于不同内容")
	}
	value.Replayed = true
	return value, nil
}

// acceptRuntimeTarget 在已锁定画布下核对固定实例与独立运行代次。
func acceptRuntimeTarget(ctx context.Context, tx *sql.Tx, input mysqlruntime.AcceptedInput) (mysqlruntime.Receipt, error) {
	var receipt mysqlruntime.Receipt
	instance, err := readRuntimeInstance(ctx, tx, input.ProjectID, true)
	if err != nil {
		return receipt, err
	}
	if instance == nil {
		if input.Command.BaseGeneration != "0" || input.Command.Desired != "running" {
			return receipt, project.Failure("RUNTIME_GENERATION_CONFLICT", "运行目标代次已改变或实例尚未建立")
		}
		instance, err = newRuntimeInstance(ctx, tx, input)
		if err != nil {
			return receipt, err
		}
	}
	if err := matchRuntimeCandidate(instance, input); err != nil {
		return receipt, err
	}
	if instance.generation == math.MaxUint64 {
		return receipt, project.Failure("RUNTIME_LIMIT", "运行目标代次已达到上限")
	}
	if err := supersedeRuntime(ctx, tx, instance, input.Command); err != nil {
		return receipt, err
	}
	opID, err := project.NewID()
	if err != nil {
		return receipt, err
	}
	targetID, err := project.NewID()
	if err != nil {
		return receipt, err
	}
	now := time.Now().UTC()
	target := mysqlruntime.Target{ID: targetID, ProjectID: input.ProjectID, CanvasRevision: input.Command.BaseRevision, Generation: strconv.FormatUint(instance.generation+1, 10), Desired: input.Command.Desired, Resource: instance.resource, CreatedAt: mysqlruntime.Timestamp(now)}
	if err := insertRuntimeTarget(ctx, tx, target, opID, input.Command, now); err != nil {
		return receipt, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_instances SET generation=?,current_operation_id=?,observation_code='TARGET_PENDING' WHERE project_id=?", target.Generation, opID, input.ProjectID); err != nil {
		return receipt, err
	}
	if err := appendRuntimeEvent(ctx, tx, input.ProjectID, opID, "target_accepted", "ACCEPTED", now); err != nil {
		return receipt, err
	}
	return mysqlruntime.Receipt{SchemaVersion: 1, MutationID: input.Command.MutationID, OperationID: opID, Target: target}, nil
}

// newRuntimeInstance 先登记稳定资源身份；没有元数据库提交就不会创建任何资源。
func newRuntimeInstance(ctx context.Context, tx *sql.Tx, input mysqlruntime.AcceptedInput) (*runtimeInstanceRow, error) {
	var installation, daemon, component string
	if err := tx.QueryRowContext(ctx, "SELECT installation_id,daemon_id FROM runtime_installations WHERE singleton=1").Scan(&installation, &daemon); err != nil {
		return nil, project.Failure("RUNTIME_INSTALLATION_CONFLICT", "运行安装尚未核实，未接受目标")
	}
	if installation != input.Resource.InstallationID || daemon != input.Resource.DaemonID {
		return nil, project.Failure("RUNTIME_INSTALLATION_CONFLICT", "运行安装与固定环境不一致")
	}
	err := tx.QueryRowContext(ctx, "SELECT id FROM middlewares WHERE id=? AND project_id=?", input.ComponentID, input.ProjectID).Scan(&component)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.Failure("COMPONENT_NOT_FOUND", "MySQL组件不属于此项目")
	}
	if err != nil {
		return nil, err
	}
	id, err := project.NewID()
	if err != nil {
		return nil, err
	}
	resource := input.Resource.WithID(id)
	data, err := json.Marshal(resource)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_instances(project_id,component_id,installation_id,resource_id,resource_json,generation,current_operation_id) VALUES(?,?,?,?,?,0,'')`, input.ProjectID, input.ComponentID, installation, id, data)
	if err != nil {
		return nil, err
	}
	return &runtimeInstanceRow{projectID: input.ProjectID, componentID: input.ComponentID, installationID: installation, resourceID: id, resource: resource}, nil
}

// matchRuntimeCandidate 防止普通新目标偷换已建立的数据身份和运行配置。
func matchRuntimeCandidate(instance *runtimeInstanceRow, input mysqlruntime.AcceptedInput) error {
	if strconv.FormatUint(instance.generation, 10) != input.Command.BaseGeneration {
		return project.Failure("RUNTIME_GENERATION_CONFLICT", "运行目标已被另一请求更新，请核实最新目标")
	}
	if instance.componentID != input.ComponentID {
		return project.Failure("RUNTIME_COMPONENT_CONFLICT", "首版每项目只管理一个固定MySQL组件身份")
	}
	if instance.installationID != input.Resource.InstallationID || instance.resource.DaemonID != input.Resource.DaemonID {
		return project.Failure("RUNTIME_INSTALLATION_CONFLICT", "运行资源不属于当前安装环境")
	}
	if instance.resource.ConfigurationHash != input.Resource.ConfigurationHash || instance.resource.Definition != input.Resource.Definition || instance.resource.Image != input.Resource.Image {
		return project.Failure("RUNTIME_CONFIGURATION_LOCKED", "运行配置已固定，草稿改变不会自动升级或重建数据库")
	}
	return nil
}

// supersedeRuntime 保留旧操作及未知步骤，绝不以新目标擦掉在途副作用。
func supersedeRuntime(ctx context.Context, tx *sql.Tx, instance *runtimeInstanceRow, command mysqlruntime.Command) error {
	if instance.operationID == "" {
		if command.RetryOf != "" {
			return project.Failure("RUNTIME_RETRY_CONFLICT", "尚无可关联的原运行操作")
		}
		return nil
	}
	if command.RetryOf != "" && command.RetryOf != instance.operationID {
		return project.Failure("RUNTIME_RETRY_CONFLICT", "恢复请求必须关联当前原操作")
	}
	op, err := readRuntimeOperation(ctx, tx, instance.projectID, instance.operationID)
	if err != nil {
		return err
	}
	if mysqlruntime.Terminal(op.value.State) {
		return nil
	}
	current, err := readRuntimeTarget(ctx, tx, instance.projectID, strconv.FormatUint(instance.generation, 10))
	if err != nil {
		return err
	}
	if current.Desired == command.Desired {
		return project.Failure("RUNTIME_BUSY", "相同目标仍在执行，请查询原操作或按原请求核实")
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_operations SET state='superseded',version=version+1,code='TARGET_SUPERSEDED',message='已有新的明确目标，原在途结果仍需核实',updated_at=? WHERE id=?", now, instance.operationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_steps SET state='unknown',code='TARGET_SUPERSEDED',updated_at=? WHERE operation_id=? AND state='executing'", now, instance.operationID); err != nil {
		return err
	}
	return appendRuntimeEvent(ctx, tx, instance.projectID, instance.operationID, "target_superseded", "TARGET_SUPERSEDED", now)
}

// insertRuntimeTarget 固定所有外部步骤，响应丢失后仍有完整资源计划可查。
func insertRuntimeTarget(ctx context.Context, tx *sql.Tx, target mysqlruntime.Target, operationID string, command mysqlruntime.Command, now time.Time) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO runtime_targets(id,project_id,generation,desired,payload,created_at) VALUES(?,?,?,?,?,?)", target.ID, target.ProjectID, target.Generation, target.Desired, data, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_operations(id,mutation_id,project_id,generation,target_id,state,next_attempt,retry_of,created_at,updated_at) VALUES(?,?,?,?,?,'accepted',?,?,?,?)`, operationID, command.MutationID, target.ProjectID, target.Generation, target.ID, now, command.RetryOf, now, now); err != nil {
		return err
	}
	steps := []string{"preflight", "credentials", "network", "volume", "container", "readiness"}
	if target.Desired == "stopped" {
		steps = []string{"preflight", "stop", "stopped"}
	}
	for _, name := range steps {
		if _, err := tx.ExecContext(ctx, "INSERT INTO runtime_steps(operation_id,name,state,updated_at) VALUES(?,?,'pending',?)", operationID, name, now); err != nil {
			return err
		}
	}
	return nil
}

// appendRuntimeEvent 在已锁定实例下推进序号；事件代次取原操作，迟到旧步骤不会冒用新目标代次。
func appendRuntimeEvent(ctx context.Context, tx *sql.Tx, id, operationID, kind, code string, now time.Time) error {
	if len(kind) > 32 || len(code) > 80 {
		return fmt.Errorf("运行事件违反诊断边界")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runtime_instances SET sequence=sequence+1 WHERE project_id=?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO runtime_events(project_id,sequence,operation_id,generation,kind,code,created_at) SELECT i.project_id,i.sequence,o.id,o.generation,?,?,? FROM runtime_instances i JOIN runtime_operations o ON o.project_id=i.project_id AND o.id=? WHERE i.project_id=?`, kind, code, now, operationID, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.Join(fmt.Errorf("运行事件缺少匹配的原操作"), err)
	}
	return appendDomainStateEvent(ctx, tx, id, "runtime", operationID)
}

// acceptRuntimeRequest 在持有项目事务后完成单一状态转换。
func acceptRuntimeRequest(ctx context.Context, tx *sql.Tx, input mysqlruntime.AcceptedInput) (mysqlruntime.Receipt, error) {
	_, err := tx.ExecContext(ctx, "INSERT INTO runtime_receipts(id,intent_hash,created_at) VALUES(?,?,?)", input.Command.MutationID, input.Hash[:], time.Now().UTC())
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return replayRuntime(ctx, tx, input.Command.MutationID, input.Hash)
	}
	if err != nil {
		return mysqlruntime.Receipt{}, err
	}
	if err := lockRevision(ctx, tx, input.ProjectID, input.Command.BaseRevision); err != nil {
		return mysqlruntime.Receipt{}, err
	}
	if err := projectRunPermission(ctx, tx, input.ProjectID); err != nil {
		return mysqlruntime.Receipt{}, err
	}
	var deploying int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployment_instances i JOIN deployment_operations o ON i.current_operation_id=o.id WHERE i.project_id=? AND o.state IN ('accepted','executing')", input.ProjectID).Scan(&deploying); err != nil {
		return mysqlruntime.Receipt{}, err
	}
	if deploying != 0 {
		return mysqlruntime.Receipt{}, project.Failure("DEPLOY_BUSY", "业务部署仍在持有原MySQL，请先核实原操作再变更数据库运行目标")
	}
	value, err := acceptRuntimeTarget(ctx, tx, input)
	if err != nil {
		return value, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE runtime_receipts SET response=? WHERE id=?", data, input.Command.MutationID)
	return value, err
}
