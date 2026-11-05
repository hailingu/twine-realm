// 本文件以一致数据库快照读取项目，不从浏览器或工作区反推已提交状态。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// queryer 让同一投影读取可用于只读快照和原子写事务。
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// List 从数据库读取项目摘要；空项目列表仍是明确的空数组。
func (store *Store) List(ctx context.Context) (summaries []project.Summary, err error) {
	rows, err := store.db.QueryContext(ctx, `SELECT p.id,p.name,p.revision,p.updated_at,COUNT(s.id) FROM projects p LEFT JOIN services s ON s.project_id=p.id GROUP BY p.id ORDER BY p.updated_at DESC,p.id`)
	if err != nil {
		return nil, fmt.Errorf("读取项目列表失败: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	summaries = []project.Summary{}
	for rows.Next() {
		var summary project.Summary
		var revision uint64
		var updated time.Time
		if err := rows.Scan(&summary.ID, &summary.Name, &revision, &updated, &summary.NodeCount); err != nil {
			return nil, err
		}
		summary.Revision, summary.UpdatedAt = strconv.FormatUint(revision, 10), updated.UTC().Format(time.RFC3339Nano)
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// Get 用单一可重复读快照恢复完整项目，防止节点与版本跨提交混合。
func (store *Store) Get(ctx context.Context, id string) (value project.Project, err error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return value, err
	}
	defer func() {
		rollback := tx.Rollback()
		if !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	value, err = readProject(ctx, tx, id)
	if err != nil {
		return value, err
	}
	return value, tx.Commit()
}

// Receipt 查询已提交回执；事务内尚未完成的请求不会暴露为空成功。
func (store *Store) Receipt(ctx context.Context, id string) (project.Receipt, [32]byte, error) {
	return readReceipt(ctx, store.db, id)
}

// readReceipt 同时恢复原意图摘要，保护同键异内容请求。
func readReceipt(ctx context.Context, database queryer, id string) (project.Receipt, [32]byte, error) {
	var receipt project.Receipt
	var hash [32]byte
	var storedHash, data []byte
	err := database.QueryRowContext(ctx, "SELECT intent_hash,response FROM mutation_receipts WHERE id=?", id).Scan(&storedHash, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, hash, project.Failure("MUTATION_NOT_FOUND", "尚未找到该请求的已提交回执，可使用原请求重试")
	}
	if err != nil {
		return receipt, hash, err
	}
	if len(storedHash) != len(hash) || len(data) == 0 {
		return receipt, hash, fmt.Errorf("持久回执不满足完整性约束")
	}
	copy(hash[:], storedHash)
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, hash, fmt.Errorf("读取持久回执失败: %w", err)
	}
	return receipt, hash, nil
}

// readProject 只读当前事务看到的聚合和具名节点信息。
func readProject(ctx context.Context, database queryer, id string) (value project.Project, err error) {
	var revision uint64
	var created, updated time.Time
	err = database.QueryRowContext(ctx, "SELECT id,name,revision,created_at,updated_at FROM projects WHERE id=?", id).Scan(&value.ID, &value.Name, &revision, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return value, project.Failure("PROJECT_NOT_FOUND", "项目不存在")
	}
	if err != nil {
		return value, err
	}
	value.SchemaVersion, value.Revision, value.Nodes = 1, strconv.FormatUint(revision, 10), []project.Node{}
	value.CreatedAt, value.UpdatedAt = created.UTC().Format(time.RFC3339Nano), updated.UTC().Format(time.RFC3339Nano)
	rows, err := database.QueryContext(ctx, `SELECT s.id,s.display_name,r.id,r.path,s.head_commit,s.position_x,s.position_y FROM services s JOIN repositories r ON r.id=s.repository_id WHERE s.project_id=? ORDER BY s.id`, id)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var node project.Node
		if err := rows.Scan(&node.ID, &node.DisplayName, &node.Repository.ID, &node.Repository.Path, &node.Repository.HeadCommit, &node.Position.X, &node.Position.Y); err != nil {
			return value, err
		}
		value.Nodes = append(value.Nodes, node)
	}
	return value, rows.Err()
}
