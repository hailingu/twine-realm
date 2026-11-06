//go:build integration

// 本文件以真实SQL核实分页、时间/数量窗口及安全错误，不删除永久领域事件。
package httpapi_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// TestProjectStateWindowAndPaging 201次真实提交经过200条页边界且不会跳号。
func TestProjectStateWindowAndPaging(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	for number := range 201 {
		command := project.LayoutCommand{MutationID: id(t), BaseRevision: current.Revision, Positions: []project.NodePosition{}}
		value, err := f.database.SaveLayout(context.Background(), current.ID, command, [32]byte{})
		if err != nil {
			t.Fatal("窗口提交失败", number, err)
		}
		current = value.Project
	}
	page, err := f.database.ProjectEvents(context.Background(), current.ID, "0")
	if err != nil || len(page.Events) != 200 || !page.HasMore || page.Next != "200" || page.Head != "202" {
		t.Fatal("第一页未遵循固定200条上限", err)
	}
	assertStateEvents(t, page.Events, current.ID)
	last, err := f.database.ProjectEvents(context.Background(), current.ID, page.Next)
	if err != nil || len(last.Events) != 2 || last.HasMore || last.Next != "202" || last.Events[0].Sequence != "201" {
		t.Fatal("后续页游标错误", err)
	}
	db := stateSQL(t, f)
	if _, err := db.Exec("UPDATE project_state_events SET created_at=? WHERE project_id=? AND sequence<=201", time.Now().UTC().Add(-25*time.Hour), current.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.database.ProjectEvents(context.Background(), current.ID, "200")
	requireStateError(t, err, "STATE_CURSOR_EXPIRED")
	_, err = f.database.ProjectEvents(context.Background(), current.ID, "203")
	requireStateError(t, err, "STATE_CURSOR_AHEAD")
	fresh := stateSnapshot(t, f, current.ID)
	if fresh.ReplayFloor != "201" || fresh.Cursor != "202" {
		t.Fatal("无新写入时未执行时间窗口")
	}
	if _, err := db.Exec("DELETE FROM project_state_events WHERE project_id=? AND sequence=202", current.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.database.ProjectEvents(context.Background(), current.ID, "201")
	requireStateError(t, err, "STATE_EVENT_GAP")
}

// insertStateWindowHistory 批量生成真实持久窗口夹具，不冒称100000次用户编辑或资源操作。
func insertStateWindowHistory(t *testing.T, f *fixture, id string) {
	t.Helper()
	db := stateSQL(t, f)
	digits := "(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	from := strings.Join([]string{digits + " a", digits + " b", digits + " c", digits + " d", digits + " e"}, " CROSS JOIN ")
	query := `INSERT INTO project_state_events(project_id,sequence,payload,created_at) SELECT ?,n,JSON_OBJECT('schemaVersion',1,'id',CONCAT(?,':',n),'projectId',?,'sequence',CAST(n AS CHAR),'scope','project','revision','0','reference',NULL,'occurredAt',?),UTC_TIMESTAMP(6) FROM (SELECT 2+a.n+10*b.n+100*c.n+1000*d.n+10000*e.n AS n FROM ` + from + `) numbers`
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(query, id, id, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE project_state_heads SET sequence=100001 WHERE project_id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestProjectStateCountRetention 真实100001条窗口越界后，仅保留100000条及明确floor。
func TestProjectStateCountRetention(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	insertStateWindowHistory(t, f, current.ID)
	command := project.LayoutCommand{MutationID: id(t), BaseRevision: "0", Positions: []project.NodePosition{}}
	if _, err := f.database.SaveLayout(context.Background(), current.ID, command, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	value := stateSnapshot(t, f, current.ID)
	if value.Cursor != "100002" || value.ReplayFloor != "2" || value.Project.Revision != "1" {
		t.Fatal("数量窗口与画布版本混淆")
	}
	db := stateSQL(t, f)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM project_state_events WHERE project_id=?", current.ID).Scan(&count); err != nil || count != 100000 {
		t.Fatal("数量窗口没有严格裁剪", count, err)
	}
	_, err := f.database.ProjectEvents(context.Background(), current.ID, "1")
	requireStateError(t, err, "STATE_CURSOR_EXPIRED")
	page, err := f.database.ProjectEvents(context.Background(), current.ID, "2")
	if err != nil || len(page.Events) != 200 || page.Events[0].Sequence != "3" {
		t.Fatal("floor边界不能连续重放", err)
	}
	assertStateEvents(t, page.Events, current.ID)
}

// TestProjectStateHTTPFailures 未知项目、非法/过期/未来游标和存储断连保留安全错误。
func TestProjectStateHTTPFailures(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	url := "/api/v5/projects/" + current.ID + "/state"
	for _, path := range []string{url + "/events?after=00", url + "/events?after=18446744073709551616", url + "/events?after=1&token=secret", url + "/events"} {
		decode[any](t, call(t, f.handler, "GET", path, nil), 400, "ErrorResponse")
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v5/projects/"+id(t)+"/state", nil), 404, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v5/projects/bad/state", nil), 400, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", url+"/events?after=2", nil), 409, "ErrorResponse")
	db := stateSQL(t, f)
	if _, err := db.Exec("UPDATE project_state_events SET created_at=? WHERE project_id=?", time.Now().UTC().Add(-25*time.Hour), current.ID); err != nil {
		t.Fatal(err)
	}
	decode[any](t, call(t, f.handler, "GET", url+"/events?after=0", nil), 409, "ErrorResponse")
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	decode[any](t, call(t, f.handler, "GET", url, nil), 503, "ErrorResponse")
	f.reopen(t)
	if value := stateSnapshot(t, f, current.ID); value.Cursor != "1" {
		t.Fatal("临时断连丢失持久游标")
	}
}
