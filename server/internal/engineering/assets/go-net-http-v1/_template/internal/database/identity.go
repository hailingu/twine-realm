// 本文件将只读数据访问与迁移执行分开，常驻应用不能借探针执行 DDL。
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"example.invalid/service/internal/schema"
)

// Store 是只读最小示例的持久化边界，不暴露驱动连接串。
type Store struct {
	db  *sql.DB
	set schema.Set
}

// NewStore 固定本次二进制的迁移清单，不从远端加载 schema。
func NewStore(db *sql.DB) (*Store, error) {
	set, err := schema.Load()
	if err != nil {
		return nil, err
	}
	return &Store{db: db, set: set}, nil
}

// Ready 进行有界认证访问，并核实当前版本与摘要，而非只判断 TCP 已开放。
func (store *Store) Ready(ctx context.Context) error {
	state, err := schema.Inspect(ctx, store.db, store.set)
	if err != nil || !state.Ready(store.set) {
		return fmt.Errorf("数据库或迁移尚未就绪")
	}
	return nil
}

// Identity 返回初始化示例的稳定时间，不返回账号、地址或秘密。
func (store *Store) Identity(ctx context.Context) (time.Time, error) {
	var initialized time.Time
	if err := store.db.QueryRowContext(ctx, "SELECT initialized_at FROM service_identity WHERE id=1").Scan(&initialized); err != nil {
		return initialized, fmt.Errorf("服务初始化信息暂不可用")
	}
	return initialized.UTC(), nil
}
