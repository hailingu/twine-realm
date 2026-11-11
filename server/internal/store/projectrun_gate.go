// 本文件统一组件许可与父观察代次，避免迟到采样覆盖生命周期转换。
package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// projectRunPermission 在已持有项目锁的事务中核实组件写许可。
func projectRunPermission(ctx context.Context, db queryer, id string) error {
	var desired, state string
	err := db.QueryRowContext(ctx, "SELECT desired,state FROM projectrun_instances WHERE project_id=?", id).Scan(&desired, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if desired != "running" || state != "succeeded" {
		return project.Failure("PROJECT_RUN_BUSY", "项目父目标尚未恢复稳定运行")
	}
	return nil
}

// projectRunEpoch 与组件目标一起采集，零表示尚无父目标。
func projectRunEpoch(ctx context.Context, db queryer, id string) (string, error) {
	var generation uint64
	err := db.QueryRowContext(ctx, "SELECT observation_epoch FROM projectrun_instances WHERE project_id=?", id).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "0", nil
	}
	return strconv.FormatUint(generation, 10), err
}
