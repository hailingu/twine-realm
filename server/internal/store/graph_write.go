// 本文件保存经过最终图校验的候选，所有声明、版本与恢复回执共用元数据库事务。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// EditGraph 在版本锁内保存差量；绑定删除只记录历史，不触碰真实资源。
func (store *Store) EditGraph(ctx context.Context, id string, command project.GraphEditCommand, candidate project.GraphState, hash [32]byte) (project.Receipt, error) {
	return store.atomicReceipt(ctx, command.MutationID, hash, "editGraph", func(tx *sql.Tx) (project.Receipt, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Receipt{}, err
		}
		before, err := readGraphState(ctx, tx, id)
		if err != nil {
			return project.Receipt{}, err
		}
		if err := saveServiceContracts(ctx, tx, id, before, candidate); err != nil {
			return project.Receipt{}, err
		}
		if err := saveBindings(ctx, tx, id, before.Bindings, candidate.Bindings); err != nil {
			return project.Receipt{}, err
		}
		value, err := advance(ctx, tx, id)
		if err != nil {
			return project.Receipt{}, err
		}
		confirmed, err := readGraphState(ctx, tx, id)
		return project.Receipt{Project: value, Graph: &confirmed}, err
	})
}

// saveServiceContracts 只处理活动版本变化，避免一次连线重复写入所有契约。
func saveServiceContracts(ctx context.Context, tx *sql.Tx, id string, before, after project.GraphState) error {
	previous := map[string]project.DefinitionRef{}
	for _, value := range before.ServiceContracts {
		previous[value.ServiceID] = value.Reference
	}
	for _, value := range after.ServiceContracts {
		if previous[value.ServiceID] == value.Reference {
			continue
		}
		if err := saveContractVersion(ctx, tx, id, value); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_contracts(service_id,project_id,contract_id,version) VALUES(?,?,?,?) AS incoming ON DUPLICATE KEY UPDATE contract_id=incoming.contract_id,version=incoming.version`, value.ServiceID, id, value.Reference.ID, value.Reference.Version); err != nil {
			return err
		}
	}
	return nil
}

// saveContractVersion 永久核对每服务的 id/version，不能通过先切走再切回来改义。
func saveContractVersion(ctx context.Context, tx *sql.Tx, id string, value project.ServiceContract) error {
	var digest string
	err := tx.QueryRowContext(ctx, "SELECT digest FROM service_contract_versions WHERE service_id=? AND contract_id=? AND version=?", value.ServiceID, value.Reference.ID, value.Reference.Version).Scan(&digest)
	if err == nil {
		if digest != value.Reference.Digest {
			return project.Failure("CONTRACT_VERSION_CONFLICT", "此服务已有相同契约版本的其他内容，请发布新版本")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	payload, err := json.Marshal(value.Document)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO service_contract_versions(service_id,project_id,contract_id,version,digest,payload) VALUES(?,?,?,?,?,?)", value.ServiceID, id, value.Reference.ID, value.Reference.Version, value.Reference.Digest, payload)
	return err
}

// saveBindings 用原集合决定新增、重连和退休，未变化关系不被重写。
func saveBindings(ctx context.Context, tx *sql.Tx, id string, before, after []project.DatabaseBinding) error {
	previous, current := map[string]project.DatabaseBinding{}, map[string]bool{}
	for _, binding := range before {
		previous[binding.ID] = binding
	}
	for _, binding := range after {
		current[binding.ID] = true
		old, exists := previous[binding.ID]
		if exists && old == binding {
			continue
		}
		if err := writeBinding(ctx, tx, id, binding, exists); err != nil {
			return err
		}
	}
	for _, binding := range before {
		if current[binding.ID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE database_bindings SET deleted_at=UTC_TIMESTAMP(6) WHERE id=? AND project_id=? AND deleted_at IS NULL", binding.ID, id); err != nil {
			return err
		}
	}
	return nil
}

// writeBinding 只采用固定 SQL；端点和值来自已校验的完整候选，不拼接用户 SQL。
func writeBinding(ctx context.Context, tx *sql.Tx, id string, binding project.DatabaseBinding, existing bool) error {
	source, err := json.Marshal(binding.SourceDefinition)
	if err != nil {
		return err
	}
	target, err := json.Marshal(binding.TargetDefinition)
	if err != nil {
		return err
	}
	values := []any{binding.Source.ComponentID, binding.Source.PortID, binding.Target.ComponentID, binding.Target.PortID, source, target, binding.Protocol, binding.Contract, binding.DatabaseID, binding.DatabaseName, binding.Purpose, binding.Credential.ID, binding.Credential.Version}
	statement := `INSERT INTO database_bindings(source_service_id,source_port,target_middleware_id,target_port,source_definition,target_definition,protocol,contract_id,database_id,database_name,purpose,credential_id,credential_version,id,project_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if existing {
		statement = `UPDATE database_bindings SET source_service_id=?,source_port=?,target_middleware_id=?,target_port=?,source_definition=?,target_definition=?,protocol=?,contract_id=?,database_id=?,database_name=?,purpose=?,credential_id=?,credential_version=? WHERE id=? AND project_id=? AND deleted_at IS NULL`
	}
	_, err = tx.ExecContext(ctx, statement, append(values, binding.ID, id)...)
	return err
}
