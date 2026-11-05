// 本文件用实际数据库锁保护迁移内容证据与工具历史之间的临界区。
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// acquire 保持明确连接身份；调用者先关闭迁移器、再释放此锁和连接池。
func acquire(ctx context.Context, db *sql.DB, name string) (func() error, error) {
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("迁移数据库尚未就绪")
	}
	digest := sha256.Sum256([]byte(name))
	key := "service-integrity-" + hex.EncodeToString(digest[:16])
	var acquired sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK(?,10)", key).Scan(&acquired); err != nil || !acquired.Valid || acquired.Int64 != 1 {
		return nil, errors.Join(fmt.Errorf("迁移锁未取得，请稍后核实"), connection.Close())
	}
	return func() error {
		releaseContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released sql.NullInt64
		err := connection.QueryRowContext(releaseContext, "SELECT RELEASE_LOCK(?)", key).Scan(&released)
		if err != nil || !released.Valid || released.Int64 != 1 {
			return errors.Join(fmt.Errorf("迁移锁释放未能核实"), connection.Close())
		}
		return connection.Close()
	}, nil
}
