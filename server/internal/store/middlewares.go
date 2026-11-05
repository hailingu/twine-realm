// 本文件原子保存标准组件草稿、统一布局和可恢复回执，不执行外部资源操作。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// PutMiddleware 在同一项目版本锁下创建或修改，定义身份不能借配置编辑偷换。
func (store *Store) PutMiddleware(ctx context.Context, id, componentID string, command project.MiddlewareCommand, reasons []string, hash [32]byte) (project.Receipt, error) {
	kind := "addMiddleware"
	if componentID != "" {
		kind = "updateMiddleware"
	}
	return store.atomicReceipt(ctx, command.MutationID, hash, kind, func(tx *sql.Tx) (project.Receipt, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Receipt{}, err
		}
		configuration, err := json.Marshal(command.Configuration)
		if err != nil {
			return project.Receipt{}, err
		}
		notReady, err := json.Marshal(reasons)
		if err != nil {
			return project.Receipt{}, err
		}
		if componentID == "" {
			err = insertMiddleware(ctx, tx, id, command, configuration, notReady)
		} else {
			err = updateMiddleware(ctx, tx, id, componentID, command, configuration, notReady)
		}
		if err != nil {
			return project.Receipt{}, err
		}
		return canvasReceipt(ctx, tx, id)
	})
}

// insertMiddleware 分配新稳定身份，显示名与配置不参与实体去重。
func insertMiddleware(ctx context.Context, tx *sql.Tx, id string, command project.MiddlewareCommand, configuration, reasons []byte) error {
	count, err := componentCount(ctx, tx, id)
	if err != nil {
		return err
	}
	if count >= 500 {
		return project.Failure("PROJECT_FULL", "当前项目最多支持 500 个组件")
	}
	componentID, err := project.NewID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO middlewares(id,project_id,definition_id,definition_version,definition_digest,display_name,configuration,not_ready_reasons,position_x,position_y) VALUES(?,?,?,?,?,?,?,?,?,?)`, componentID, id, command.Definition.ID, command.Definition.Version, command.Definition.Digest, command.DisplayName, configuration, reasons, command.Position.X, command.Position.Y)
	return err
}

// updateMiddleware 保留实例归属与定义版本，升级需要未来单独的显式变更入口。
func updateMiddleware(ctx context.Context, tx *sql.Tx, id, componentID string, command project.MiddlewareCommand, configuration, reasons []byte) error {
	var reference project.DefinitionRef
	err := tx.QueryRowContext(ctx, "SELECT definition_id,definition_version,definition_digest FROM middlewares WHERE id=? AND project_id=?", componentID, id).Scan(&reference.ID, &reference.Version, &reference.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Failure("COMPONENT_NOT_FOUND", "组件不存在或不属于此项目")
	}
	if err != nil {
		return err
	}
	if reference != command.Definition {
		return project.Failure("DEFINITION_CONFLICT", "配置编辑不能改变已有组件的定义版本")
	}
	_, err = tx.ExecContext(ctx, "UPDATE middlewares SET display_name=?,configuration=?,not_ready_reasons=?,position_x=?,position_y=? WHERE id=? AND project_id=?", command.DisplayName, configuration, reasons, command.Position.X, command.Position.Y, componentID, id)
	return err
}

// SaveCanvasLayout 对最终成员集合整体校验，再保存所有服务与中间件坐标。
func (store *Store) SaveCanvasLayout(ctx context.Context, id string, command project.CanvasLayoutCommand, hash [32]byte) (project.Receipt, error) {
	return store.atomicReceipt(ctx, command.MutationID, hash, "saveCanvasLayout", func(tx *sql.Tx) (project.Receipt, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Receipt{}, err
		}
		members, err := canvasMembers(ctx, tx, id)
		if err != nil {
			return project.Receipt{}, err
		}
		if len(members) != len(command.Positions) {
			return project.Receipt{}, project.Failure("INVALID_LAYOUT", "位置集合必须恰好覆盖全部画布组件")
		}
		if err := writeCanvasPositions(ctx, tx, id, members, command.Positions); err != nil {
			return project.Receipt{}, err
		}
		return canvasReceipt(ctx, tx, id)
	})
}

// writeCanvasPositions 仅按服务端已确认的成员类别选择固定 SQL；错误由外层事务整批回滚。
func writeCanvasPositions(ctx context.Context, tx *sql.Tx, id string, members map[string]string, positions []project.CanvasPosition) error {
	for _, position := range positions {
		kind, exists := members[position.ComponentID]
		if !exists {
			return project.Failure("INVALID_LAYOUT", "位置集合包含不存在或其他项目组件")
		}
		statement := "UPDATE services SET position_x=?,position_y=? WHERE id=? AND project_id=?"
		if kind == "mysql" {
			statement = "UPDATE middlewares SET position_x=?,position_y=? WHERE id=? AND project_id=?"
		}
		if _, err := tx.ExecContext(ctx, statement, position.X, position.Y, position.ComponentID, id); err != nil {
			return err
		}
	}
	return nil
}

// canvasMembers 在已锁项目内解析成员类别，不接受客户端决定表名。
func canvasMembers(ctx context.Context, tx *sql.Tx, id string) (map[string]string, error) {
	value, err := readProject(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	canvas, err := readCanvasState(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, node := range value.Nodes {
		result[node.ID] = "service"
	}
	for _, node := range canvas.Middlewares {
		result[node.ID] = node.Kind
	}
	return result, nil
}
