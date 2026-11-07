// 本文件在一个 RR 元数据边界读取完整信封，不拼接多个独立快照。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// ProjectState 保证画布、版本指针、状态与游标属于同一次平台数据库快照。
func (store *Store) ProjectState(ctx context.Context, id string) (projectstate.Snapshot, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (projectstate.Snapshot, error) { return readProjectState(ctx, tx, id) })
}

// readProjectState 复用各模块内部读函数，避免另开事务或访问外部资源。
func readProjectState(ctx context.Context, database queryer, id string) (value projectstate.Snapshot, err error) {
	value = projectstate.Snapshot{SchemaVersion: 1, Builds: []servicebuild.Snapshot{}, Deployments: []servicedeploy.Snapshot{}, Policy: projectstate.DefaultPolicy()}
	value.Project, err = readProject(ctx, database, id)
	if err != nil {
		return value, err
	}
	value.Graph, err = readGraphState(ctx, database, id)
	if err != nil {
		return value, err
	}
	value.Runtime, err = readRuntimeSnapshot(ctx, database, id)
	if err != nil {
		return value, err
	}
	for _, node := range value.Project.Nodes {
		build, err := readBuildSnapshot(ctx, database, id, node.ID)
		if err != nil {
			return value, err
		}
		deployment, err := readDeploymentSnapshot(ctx, database, id, node.ID)
		if err != nil {
			return value, err
		}
		value.Builds = append(value.Builds, build)
		value.Deployments = append(value.Deployments, deployment)
	}
	head, floor, err := readStateWindow(ctx, database, id, time.Now().UTC())
	value.Cursor, value.ReplayFloor = strconv.FormatUint(head, 10), strconv.FormatUint(floor, 10)
	value.CapturedAt = projectstate.Timestamp(time.Now())
	return value, err
}

// readStateWindow 即使项目长期没有写入，也会拒绝已超过时间窗口的旧游标。
func readStateWindow(ctx context.Context, database queryer, id string, now time.Time) (head, floor uint64, err error) {
	err = database.QueryRowContext(ctx, "SELECT sequence,replay_floor FROM project_state_heads WHERE project_id=?", id).Scan(&head, &floor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var expired uint64
	err = database.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM project_state_events WHERE project_id=? AND created_at<?", id, now.Add(-time.Duration(projectstate.DefaultPolicy().RetentionSeconds)*time.Second)).Scan(&expired)
	if expired > floor {
		floor = expired
	}
	if floor > head {
		return 0, 0, fmt.Errorf("项目通知窗口记录不一致")
	}
	return head, floor, err
}

// ProjectEvents 同时核实窗口和连续事件，不以缺行或未来游标冒充追平。
func (store *Store) ProjectEvents(ctx context.Context, id, after string) (projectstate.Page, error) {
	return runtimeRead(ctx, store.db, func(tx *sql.Tx) (projectstate.Page, error) { return readProjectEvents(ctx, tx, id, after) })
}

// readProjectEvents 有界读取不永久占用连接，SSE 等待发生在事务之外。
func readProjectEvents(ctx context.Context, database queryer, id, after string) (value projectstate.Page, err error) {
	value = projectstate.Page{Events: []projectstate.Event{}, Next: after}
	if err := runtimeProjectExists(ctx, database, id); err != nil {
		return value, err
	}
	cursor, err := projectstate.Cursor(after)
	if err != nil {
		return value, err
	}
	head, floor, err := readStateWindow(ctx, database, id, time.Now().UTC())
	if err != nil {
		return value, err
	}
	value.Head = strconv.FormatUint(head, 10)
	if cursor < floor {
		return value, project.Failure("STATE_CURSOR_EXPIRED", "事件游标已过期，请重新读取完整状态信封")
	}
	if cursor > head {
		return value, project.Failure("STATE_CURSOR_AHEAD", "事件游标超前，请重新核实完整状态信封")
	}
	return readStateEventRows(ctx, database, id, cursor, head)
}

// readStateEventRows 单页拥有行集并验证连续性，身份解码不与窗口判定混合。
func readStateEventRows(ctx context.Context, database queryer, id string, cursor, head uint64) (value projectstate.Page, err error) {
	value = projectstate.Page{Events: []projectstate.Event{}, Head: strconv.FormatUint(head, 10), Next: strconv.FormatUint(cursor, 10)}
	rows, err := database.QueryContext(ctx, "SELECT sequence,payload FROM project_state_events WHERE project_id=? AND sequence>? ORDER BY sequence LIMIT ?", id, cursor, projectstate.DefaultPolicy().PageSize)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var number uint64
		var payload []byte
		if err := rows.Scan(&number, &payload); err != nil {
			return value, err
		}
		if cursor == head || number != cursor+1 {
			return value, project.Failure("STATE_EVENT_GAP", "项目通知不连续，请重新读取完整状态信封")
		}
		event, err := decodeStateEvent(id, number, payload)
		if err != nil {
			return value, err
		}
		value.Events = append(value.Events, event)
		cursor = number
	}
	if err := rows.Err(); err != nil {
		return value, err
	}
	if len(value.Events) < projectstate.DefaultPolicy().PageSize && cursor < head {
		return value, project.Failure("STATE_EVENT_GAP", "项目通知缺少已提交事件，请完整恢复")
	}
	value.Next, value.HasMore = strconv.FormatUint(cursor, 10), cursor < head
	return value, nil
}

// decodeStateEvent 不从损坏载荷猜测事件归属或版本。
func decodeStateEvent(id string, number uint64, payload []byte) (event projectstate.Event, err error) {
	if err := decodeRuntimeJSON(payload, &event); err != nil {
		return event, err
	}
	if event.SchemaVersion != 1 || event.ProjectID != id || event.Sequence != strconv.FormatUint(number, 10) || event.ID != id+":"+event.Sequence {
		return event, fmt.Errorf("项目通知身份不一致")
	}
	return event, nil
}
