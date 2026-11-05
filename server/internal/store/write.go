// 本文件实现用例明确要求的原子提交：项目变更和恢复回执共用一个事务。
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// Create 仅在持久化项目和原请求回执同时提交后返回成功。
func (store *Store) Create(ctx context.Context, command project.CreateCommand, hash [32]byte) (project.Receipt, error) {
	return store.atomic(ctx, command.MutationID, hash, "createProject", func(tx *sql.Tx) (project.Project, error) {
		id, err := project.NewID()
		if err != nil {
			return project.Project{}, err
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, "INSERT INTO projects(id,name,revision,created_at,updated_at) VALUES(?,?,0,?,?)", id, command.Name, now, now); err != nil {
			return project.Project{}, err
		}
		return readProject(ctx, tx, id)
	})
}

// Register 在版本锁和唯一关联约束下登记只读观测结果。
func (store *Store) Register(ctx context.Context, id string, command project.RegisterCommand, source project.RepositorySource, hash [32]byte) (project.Receipt, error) {
	return store.atomic(ctx, command.MutationID, hash, "registerRepository", func(tx *sql.Tx) (project.Project, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Project{}, err
		}
		repositoryID, err := repositoryIdentity(ctx, tx, source.Path)
		if err != nil {
			return project.Project{}, err
		}
		if err := canRegister(ctx, tx, id, repositoryID); err != nil {
			return project.Project{}, err
		}
		serviceID, err := project.NewID()
		if err != nil {
			return project.Project{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO services(id,project_id,repository_id,display_name,head_commit,position_x,position_y) VALUES(?,?,?,?,?,?,?)`, serviceID, id, repositoryID, command.DisplayName, source.HeadCommit, command.Position.X, command.Position.Y); err != nil {
			return project.Project{}, err
		}
		return advance(ctx, tx, id)
	})
}

// SaveLayout 要求位置集合恰好覆盖当前节点，冲突或遗漏均不覆盖已有布局。
func (store *Store) SaveLayout(ctx context.Context, id string, command project.LayoutCommand, hash [32]byte) (project.Receipt, error) {
	return store.atomic(ctx, command.MutationID, hash, "saveLayout", func(tx *sql.Tx) (project.Project, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Project{}, err
		}
		current, err := readProject(ctx, tx, id)
		if err != nil {
			return current, err
		}
		if err := matchMembers(current, command.Positions); err != nil {
			return current, err
		}
		for _, position := range command.Positions {
			if _, err := tx.ExecContext(ctx, "UPDATE services SET position_x=?,position_y=? WHERE id=? AND project_id=?", position.X, position.Y, position.ServiceID, id); err != nil {
				return current, err
			}
		}
		return advance(ctx, tx, id)
	})
}

// canRegister 在已锁定的项目内检查唯一关联与一期容量。
func canRegister(ctx context.Context, tx *sql.Tx, id, repositoryID string) error {
	var existing string
	err := tx.QueryRowContext(ctx, "SELECT id FROM services WHERE project_id=? AND repository_id=?", id, repositoryID).Scan(&existing)
	if err == nil {
		return &project.Error{Code: "DUPLICATE_REPOSITORY", Message: "该仓库已加入此项目", ExistingServiceID: existing}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM services WHERE project_id=?", id).Scan(&count); err != nil {
		return err
	}
	if count >= 500 {
		return project.Failure("PROJECT_FULL", "当前项目最多支持 500 个服务")
	}
	return nil
}

// matchMembers 防止位置保存隐式删除节点或引用其他项目实体。
func matchMembers(current project.Project, positions []project.NodePosition) error {
	if len(current.Nodes) != len(positions) {
		return project.Failure("INVALID_LAYOUT", "位置集合必须包含当前项目的全部服务")
	}
	members := make(map[string]bool, len(current.Nodes))
	for _, node := range current.Nodes {
		members[node.ID] = true
	}
	for _, position := range positions {
		if !members[position.ServiceID] {
			return project.Failure("INVALID_LAYOUT", "位置集合引用了其他项目或不存在的服务")
		}
	}
	return nil
}

// atomic 把请求占位、业务写入和完整回执置于同一提交屏障。
func (store *Store) atomic(ctx context.Context, id string, hash [32]byte, kind string, action func(*sql.Tx) (project.Project, error)) (receipt project.Receipt, err error) {
	return store.atomicReceipt(ctx, id, hash, kind, func(tx *sql.Tx) (project.Receipt, error) {
		value, err := action(tx)
		return project.Receipt{Project: value}, err
	})
}

// atomicReceipt 同时保存原有画布回执与新增源码回执，不改变各自发布形状。
func (store *Store) atomicReceipt(ctx context.Context, id string, hash [32]byte, kind string, action func(*sql.Tx) (project.Receipt, error)) (receipt project.Receipt, err error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return receipt, err
	}
	defer func() {
		rollback := tx.Rollback()
		if !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	_, err = tx.ExecContext(ctx, "INSERT INTO mutation_receipts(id,intent_hash,created_at) VALUES(?,?,?)", id, hash[:], time.Now().UTC())
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return replayLocked(ctx, tx, id, hash)
	}
	if err != nil {
		return receipt, err
	}
	receipt, err = action(tx)
	if err != nil {
		return receipt, err
	}
	receipt.MutationID, receipt.Kind, receipt.Replayed = id, kind, false
	data, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE mutation_receipts SET response=? WHERE id=?", data, id); err != nil {
		return receipt, err
	}
	if err = tx.Commit(); err != nil {
		return project.Receipt{}, &project.Error{Code: "RESULT_UNKNOWN", Message: "提交结果尚未确认，请核实或用原请求重试", Cause: err}
	}
	return receipt, nil
}

// replayLocked 处理同时到达的相同请求，读取已提交的唯一回执。
func replayLocked(ctx context.Context, tx *sql.Tx, id string, hash [32]byte) (project.Receipt, error) {
	receipt, existing, err := readReceipt(ctx, tx, id)
	if err != nil {
		return receipt, err
	}
	if existing != hash {
		return project.Receipt{}, project.Failure("IDEMPOTENCY_CONFLICT", "相同请求身份已用于不同内容")
	}
	receipt.Replayed = true
	return receipt, nil
}

// lockRevision 串行化一个项目的变更，版本只在全部写入成功后推进。
func lockRevision(ctx context.Context, tx *sql.Tx, id, expected string) error {
	var revision uint64
	err := tx.QueryRowContext(ctx, "SELECT revision FROM projects WHERE id=? FOR UPDATE", id).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Failure("PROJECT_NOT_FOUND", "项目不存在")
	}
	if err != nil {
		return err
	}
	current := strconv.FormatUint(revision, 10)
	if current != expected {
		return &project.Error{Code: "REVISION_CONFLICT", Message: "项目已被其他窗口更新，草稿已保留，请载入最新版本后重新应用", CurrentRevision: current}
	}
	return nil
}

// repositoryIdentity 把规范路径作为本机定位去重键，实体身份仍为独立 UUID。
func repositoryIdentity(ctx context.Context, tx *sql.Tx, path string) (string, error) {
	id, err := project.NewID()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(path))
	if _, err := tx.ExecContext(ctx, "INSERT INTO repositories(id,path,path_hash) VALUES(?,?,?) ON DUPLICATE KEY UPDATE id=id", id, path, hash[:]); err != nil {
		return "", err
	}
	var storedPath string
	if err := tx.QueryRowContext(ctx, "SELECT id,path FROM repositories WHERE path_hash=?", hash[:]).Scan(&id, &storedPath); err != nil {
		return "", err
	}
	if storedPath != path {
		return "", fmt.Errorf("仓库定位摘要冲突")
	}
	return id, nil
}

// advance 更新聚合版本和时间并返回同一事务中的完整快照。
func advance(ctx context.Context, tx *sql.Tx, id string) (project.Project, error) {
	if _, err := tx.ExecContext(ctx, "UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?", time.Now().UTC(), id); err != nil {
		return project.Project{}, err
	}
	return readProject(ctx, tx, id)
}
