// 本文件一致读取服务、标准组件、固定契约与依赖；不查询运行资源或源码工作区。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Graph 用一个可重复读事务建立 v2 投影，不复制原服务或中间件数据。
func (store *Store) Graph(ctx context.Context, id string) (value project.GraphSnapshot, err error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return value, err
	}
	defer func() {
		if rollback := tx.Rollback(); !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	value.Project, err = readProject(ctx, tx, id)
	if err != nil {
		return value, err
	}
	value.Graph, err = readGraphState(ctx, tx, id)
	if err != nil {
		return value, err
	}
	return value, tx.Commit()
}

// readGraphState 读取既有标准组件及新增关系，空集合始终是明确的数组。
func readGraphState(ctx context.Context, database queryer, id string) (value project.GraphState, err error) {
	value.SchemaVersion = 2
	canvas, err := readCanvasState(ctx, database, id)
	if err != nil {
		return value, err
	}
	value.Middlewares = canvas.Middlewares
	value.ServiceContracts, err = readServiceContracts(ctx, database, id)
	if err != nil {
		return value, err
	}
	value.Bindings, err = readBindings(ctx, database, id)
	return value, err
}

// readServiceContracts 只选择各服务的活动版本，历史内容仍永久留在版本表核实。
func readServiceContracts(ctx context.Context, database queryer, id string) (values []project.ServiceContract, err error) {
	rows, err := database.QueryContext(ctx, `SELECT c.service_id,v.contract_id,v.version,v.digest,v.payload FROM service_contracts c JOIN service_contract_versions v ON c.service_id=v.service_id AND c.contract_id=v.contract_id AND c.version=v.version WHERE c.project_id=? ORDER BY c.service_id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	values = []project.ServiceContract{}
	for rows.Next() {
		var value project.ServiceContract
		var payload []byte
		if err := rows.Scan(&value.ServiceID, &value.Reference.ID, &value.Reference.Version, &value.Reference.Digest, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &value.Document); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// readBindings 保留已删除身份在数据库中，投影只返回当前声明的关系。
func readBindings(ctx context.Context, database queryer, id string) (values []project.DatabaseBinding, err error) {
	rows, err := database.QueryContext(ctx, `SELECT id,source_service_id,source_port,target_middleware_id,target_port,source_definition,target_definition,protocol,contract_id,database_id,database_name,purpose,credential_id,credential_version FROM database_bindings WHERE project_id=? AND deleted_at IS NULL ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	values = []project.DatabaseBinding{}
	for rows.Next() {
		value, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// scanBinding 对两个固定定义引用严格解码，不把读取失败当作无连接。
func scanBinding(rows *sql.Rows) (value project.DatabaseBinding, err error) {
	var sourceDefinition, targetDefinition []byte
	value.Kind = "databaseDependency"
	err = rows.Scan(&value.ID, &value.Source.ComponentID, &value.Source.PortID, &value.Target.ComponentID, &value.Target.PortID, &sourceDefinition, &targetDefinition, &value.Protocol, &value.Contract, &value.DatabaseID, &value.DatabaseName, &value.Purpose, &value.Credential.ID, &value.Credential.Version)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(sourceDefinition, &value.SourceDefinition); err != nil {
		return value, err
	}
	if err := json.Unmarshal(targetDefinition, &value.TargetDefinition); err != nil {
		return value, err
	}
	return value, nil
}
