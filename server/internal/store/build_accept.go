// 本文件以同库事务受理固定构建输入，不在事务中导出源码或创建容器。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// BindBuild 将唯一安装根与完整工具环境绑定元数据库，拒绝静默接管历史。
func (store *Store) BindBuild(ctx context.Context, id, environmentDigest string) error {
	_, err := runtimeWrite(ctx, store.db, func(tx *sql.Tx) (bool, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO build_installations(singleton,installation_id,environment_digest) VALUES(1,?,?) ON DUPLICATE KEY UPDATE singleton=singleton", id, environmentDigest); err != nil {
			return false, err
		}
		var existing, digest string
		if err := tx.QueryRowContext(ctx, "SELECT installation_id,environment_digest FROM build_installations WHERE singleton=1 FOR UPDATE").Scan(&existing, &digest); err != nil {
			return false, err
		}
		if existing != id || digest != environmentDigest {
			return false, project.Failure("BUILD_INSTALLATION_CONFLICT", "构建元数据库已绑定其他安装或环境，请恢复原配置核实")
		}
		return true, nil
	})
	return err
}

// AcceptBuild 原键占位、当前源码锁、不可变目标、操作和回执必须一起提交。
func (store *Store) AcceptBuild(ctx context.Context, input servicebuild.AcceptedInput) (servicebuild.Receipt, error) {
	return runtimeWrite(ctx, store.db, func(tx *sql.Tx) (servicebuild.Receipt, error) {
		return buildReceiptWrite(ctx, tx, input.Command.MutationID, input.Hash, func() (servicebuild.Receipt, error) { return acceptBuildLocked(ctx, tx, input) })
	})
}

// buildReceiptWrite 在唯一键上串行同键并发；异意图不会读取当前配置重新执行。
func buildReceiptWrite(ctx context.Context, tx *sql.Tx, id string, hash [32]byte, action func() (servicebuild.Receipt, error)) (servicebuild.Receipt, error) {
	_, err := tx.ExecContext(ctx, "INSERT INTO build_receipts(id,intent_hash,created_at) VALUES(?,?,?)", id, hash[:], time.Now().UTC())
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		value, stored, err := readBuildReceipt(ctx, tx, id)
		if err != nil {
			return value, err
		}
		if hash != stored {
			return value, project.Failure("IDEMPOTENCY_CONFLICT", "相同构建请求身份已用于不同内容")
		}
		value.Replayed = true
		return value, nil
	}
	if err != nil {
		return servicebuild.Receipt{}, err
	}
	value, err := action()
	if err != nil {
		return value, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE build_receipts SET response=? WHERE id=?", data, id)
	return value, err
}

// acceptBuildLocked 在项目锁内重新核实关联，前置只读检查不能替代提交时检查。
func acceptBuildLocked(ctx context.Context, tx *sql.Tx, input servicebuild.AcceptedInput) (servicebuild.Receipt, error) {
	var value servicebuild.Receipt
	target := input.Target
	if err := lockRevision(ctx, tx, target.ProjectID, input.Command.BaseRevision); err != nil {
		return value, err
	}
	source, err := readSource(ctx, tx, target.ProjectID, target.ServiceID)
	if err != nil {
		return value, err
	}
	if source.PinnedCommit == nil || *source.PinnedCommit != target.Source.PinnedCommit || source.RepositoryID != target.Source.RepositoryID || source.RepositoryPath != target.Source.RepositoryPath || source.ProjectRevision != target.CanvasRevision {
		return value, project.Failure("BUILD_SOURCE_CHANGED", "固定源码关联已改变，请读取最新状态再确认构建")
	}
	if err := matchBuildInstallation(ctx, tx, target); err != nil {
		return value, err
	}
	instance, err := readBuildInstance(ctx, tx, target.ProjectID, target.ServiceID, true)
	if err != nil {
		return value, err
	}
	if instance == nil {
		if _, err := tx.ExecContext(ctx, "INSERT INTO build_instances(project_id,service_id) VALUES(?,?)", target.ProjectID, target.ServiceID); err != nil {
			return value, err
		}
		instance = &buildInstance{}
	}
	if strconv.FormatUint(instance.generation, 10) != input.Command.BaseGeneration || instance.generation == math.MaxUint64 {
		return value, project.Failure("BUILD_GENERATION_CONFLICT", "构建目标代次已改变，请核实当前操作")
	}
	if err := previousBuildTerminal(ctx, tx, target, instance, input.Command.RetryOf); err != nil {
		return value, err
	}
	target.Generation = strconv.FormatUint(instance.generation+1, 10)
	operationID, err := project.NewID()
	if err != nil {
		return value, err
	}
	now := servicebuild.Timestamp(time.Now())
	op := servicebuild.Operation{ID: operationID, TargetID: target.ID, ProjectID: target.ProjectID, ServiceID: target.ServiceID, Version: "1", State: "accepted", Code: "ACCEPTED", Message: "固定构建输入已持久接受，检查尚未完成", Steps: []servicebuild.Step{}, CreatedAt: now, UpdatedAt: now}
	if err := insertBuild(ctx, tx, target, op); err != nil {
		return value, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE build_instances SET generation=?,current_operation_id=? WHERE project_id=? AND service_id=?", target.Generation, operationID, target.ProjectID, target.ServiceID)
	return servicebuild.Receipt{SchemaVersion: 1, MutationID: input.Command.MutationID, Kind: "build", OperationID: operationID, Target: target, Version: "1"}, err
}

// matchBuildInstallation 再次核实固定策略摘要与受理安装，不信任可变私有根。
func matchBuildInstallation(ctx context.Context, tx *sql.Tx, target servicebuild.Target) error {
	var installation, digest string
	if err := tx.QueryRowContext(ctx, "SELECT installation_id,environment_digest FROM build_installations WHERE singleton=1").Scan(&installation, &digest); err != nil {
		return project.Failure("BUILD_INSTALLATION_CONFLICT", "构建安装尚未核实")
	}
	expected, err := servicebuild.Fingerprint(target.Environment)
	if err != nil {
		return err
	}
	input, err := servicebuild.InputFingerprint(target.Source, target.Profile, target.Environment, target.Policy, target.InstallationID)
	if err != nil {
		return err
	}
	if installation != target.InstallationID || digest != expected || input != target.InputDigest || !reflect.DeepEqual(target.Policy, servicebuild.DefaultPolicy()) {
		return project.Failure("BUILD_INSTALLATION_CONFLICT", "固定构建输入与安装或策略不一致")
	}
	return nil
}

// previousBuildTerminal 不允许用新代次跳过仍在执行或未知取消的旧沙箱。
func previousBuildTerminal(ctx context.Context, tx *sql.Tx, target servicebuild.Target, instance *buildInstance, retryOf string) error {
	if instance.operationID == "" {
		if retryOf != "" {
			return project.Failure("BUILD_RETRY_CONFLICT", "没有可关联的原构建")
		}
		return nil
	}
	if retryOf != "" && retryOf != instance.operationID {
		return project.Failure("BUILD_RETRY_CONFLICT", "重试须关联当前原操作")
	}
	op, err := readBuildOperation(ctx, tx, target.ProjectID, target.ServiceID, instance.operationID)
	if err != nil {
		return err
	}
	if !servicebuild.Terminal(op.value.State) {
		return project.Failure("BUILD_BUSY", "原构建仍在执行或取消，请先核实原操作")
	}
	if op.value.State == "needs_attention" {
		return project.Failure("BUILD_RECOVERY_REQUIRED", "原构建存在未知外部结果，须先核实并取消原操作")
	}
	return nil
}

// insertBuild 固定完整JSON和索引身份，接受事件与目标同时存在。
func insertBuild(ctx context.Context, tx *sql.Tx, target servicebuild.Target, op servicebuild.Operation) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO build_targets(id,project_id,service_id,generation,payload) VALUES(?,?,?,?,?)", target.ID, target.ProjectID, target.ServiceID, target.Generation, data); err != nil {
		return err
	}
	data, err = json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO build_operations(id,project_id,service_id,target_id,state,payload,created_at) VALUES(?,?,?,?,?,?,?)", op.ID, op.ProjectID, op.ServiceID, op.TargetID, op.State, data, time.Now().UTC()); err != nil {
		return err
	}
	return appendBuildEvent(ctx, tx, op, "accepted", data)
}

// appendBuildEvent 保存永久操作历史，再于同事务追加独立项目通知。
func appendBuildEvent(ctx context.Context, tx *sql.Tx, op servicebuild.Operation, kind string, data []byte) error {
	if len(data) > 65536 {
		return fmt.Errorf("构建操作事件超出固定上限")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO build_events(operation_id,version,kind,payload,created_at) VALUES(?,?,?,?,?)", op.ID, op.Version, kind, data, time.Now().UTC())
	if err != nil {
		return err
	}
	return appendDomainStateEvent(ctx, tx, op.ProjectID, "build", op.ID)
}
