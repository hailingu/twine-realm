// 本文件以一致性读组合原服务和标准中间件，画布仍只有一个项目 revision。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Canvas 不把多个独立查询交给浏览器拼接，事务内的集合来自同一快照。
func (store *Store) Canvas(ctx context.Context, id string) (value project.CanvasSnapshot, err error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
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
	value.Canvas, err = readCanvasState(ctx, tx, id)
	if err != nil {
		return value, err
	}
	return value, tx.Commit()
}

// readCanvasState 读取固定定义与规范化声明，不查询 Docker 或推导实时健康。
func readCanvasState(ctx context.Context, database queryer, id string) (value project.CanvasState, err error) {
	value = project.CanvasState{SchemaVersion: 1, Middlewares: []project.MiddlewareNode{}}
	rows, err := database.QueryContext(ctx, `SELECT id,definition_id,definition_version,definition_digest,display_name,configuration,not_ready_reasons,position_x,position_y FROM middlewares WHERE project_id=? ORDER BY id`, id)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		node, err := scanMiddleware(rows)
		if err != nil {
			return value, err
		}
		value.Middlewares = append(value.Middlewares, node)
	}
	return value, rows.Err()
}

// scanMiddleware 对保存载荷再次解码，数据库损坏不能静默丢弃一个节点。
func scanMiddleware(rows *sql.Rows) (value project.MiddlewareNode, err error) {
	var configuration, reasons []byte
	value.Kind = "mysql"
	err = rows.Scan(&value.ID, &value.Definition.ID, &value.Definition.Version, &value.Definition.Digest, &value.DisplayName, &configuration, &reasons, &value.Position.X, &value.Position.Y)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(configuration, &value.Configuration); err != nil {
		return value, err
	}
	if err := json.Unmarshal(reasons, &value.NotReadyReasons); err != nil {
		return value, err
	}
	return value, nil
}

// canvasReceipt 固定原有服务与新增组件的同一事务结果，不改变旧回执的形状。
func canvasReceipt(ctx context.Context, tx *sql.Tx, id string) (project.Receipt, error) {
	value, err := advance(ctx, tx, id)
	if err != nil {
		return project.Receipt{}, err
	}
	canvas, err := readCanvasState(ctx, tx, id)
	return project.Receipt{Project: value, Canvas: &canvas}, err
}

// componentCount 以统一容量保护服务和中间件；旧登记入口不能绕过该限制。
func componentCount(ctx context.Context, tx *sql.Tx, id string) (count int, err error) {
	err = tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM services WHERE project_id=?)+(SELECT COUNT(*) FROM middlewares WHERE project_id=?)", id, id).Scan(&count)
	return count, err
}
