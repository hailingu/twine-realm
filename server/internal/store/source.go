// 本文件持久化固定源码选择，登记时 HEAD 与固定版本互不覆盖。
package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Source 用同一 SQL 读取所属关系、项目版本和已固定 commit。
func (store *Store) Source(ctx context.Context, id, serviceID string) (project.SourceVersion, error) {
	return readSource(ctx, store.db, id, serviceID)
}

// readSource 不从工作区反推已保存值，未设置值和旧数据均保持 null。
func readSource(ctx context.Context, database queryer, id, serviceID string) (project.SourceVersion, error) {
	var value project.SourceVersion
	var revision uint64
	err := database.QueryRowContext(ctx, `SELECT p.id,p.revision,s.id,r.id,r.path,s.pinned_commit FROM services s JOIN projects p ON p.id=s.project_id JOIN repositories r ON r.id=s.repository_id WHERE p.id=? AND s.id=?`, id, serviceID).Scan(&value.ProjectID, &revision, &value.ServiceID, &value.RepositoryID, &value.RepositoryPath, &value.PinnedCommit)
	if errors.Is(err, sql.ErrNoRows) {
		return value, project.Failure("SERVICE_NOT_FOUND", "服务不存在或不属于该项目")
	}
	value.ProjectRevision = strconv.FormatUint(revision, 10)
	return value, err
}

// PinSource 使固定值、项目版本与源码专用回执在同一提交屏障生效。
func (store *Store) PinSource(ctx context.Context, id, serviceID string, command project.PinSourceCommand, hash [32]byte) (project.Receipt, error) {
	return store.atomicReceipt(ctx, command.MutationID, hash, "pinSource", func(tx *sql.Tx) (project.Receipt, error) {
		if err := lockRevision(ctx, tx, id, command.BaseRevision); err != nil {
			return project.Receipt{}, err
		}
		if _, err := readSource(ctx, tx, id, serviceID); err != nil {
			return project.Receipt{}, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE services SET pinned_commit=? WHERE id=? AND project_id=?", command.Commit, serviceID, id); err != nil {
			return project.Receipt{}, err
		}
		value, err := advance(ctx, tx, id)
		if err != nil {
			return project.Receipt{}, err
		}
		version, err := readSource(ctx, tx, id, serviceID)
		return project.Receipt{Project: value, SourceVersion: &version}, err
	})
}
