//go:build integration

// 本文件穿过真实元数据库、用例与发布契约验证连续通知和原子恢复。
package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/contract"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateSnapshot 通过真实HTTP和发布schema读取完整元数据信封。
func stateSnapshot(t *testing.T, f *fixture, id string) projectstate.Snapshot {
	t.Helper()
	return decode[projectstate.Snapshot](t, call(t, f.handler, "GET", "/api/v5/projects/"+id+"/state", nil), 200, "StateSnapshot")
}

// stateSQL 使用当前临时库的独立连接，故障注入不改变用户数据库。
func stateSQL(t *testing.T, f *fixture) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", f.dsn)
	if err != nil {
		t.Fatal("临时状态验证连接失败")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

// assertStateEvents 检查每个公开字段的schema和归属，而非只看调用次数。
func assertStateEvents(t *testing.T, events []projectstate.Event, id string) {
	t.Helper()
	schemas, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var unknown any
		if err := json.Unmarshal(data, &unknown); err != nil {
			t.Fatal(err)
		}
		if err := schemas["StateEvent"].Validate(unknown); err != nil {
			t.Fatal("事件schema不匹配", err)
		}
		if event.ProjectID != id || event.ID != id+":"+event.Sequence {
			t.Fatal("事件归属不一致")
		}
	}
}

// TestProjectStateAtomicRecovery 回执重放、拒绝和控制面重开不复制状态或通知。
func TestProjectStateAtomicRecovery(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	initial := stateSnapshot(t, f, current.ID)
	if initial.Cursor != "1" || initial.Project.Revision != "0" || len(initial.Builds) != 0 || initial.Runtime.Target != nil {
		t.Fatal("初始信封包含伪造状态")
	}
	command := project.RegisterCommand{MutationID: id(t), BaseRevision: "0", RepositoryPath: repository(t, f.root, "state"), Position: project.Position{X: 1, Y: 2}}
	url := "/api/v1/projects/" + current.ID + "/services"
	registered := decode[project.Receipt](t, call(t, f.handler, "POST", url, command), 201, "Receipt")
	value := stateSnapshot(t, f, current.ID)
	if value.Cursor != "2" || value.Project.Revision != "1" || len(value.Builds) != 1 || len(value.Deployments) != 1 || value.Builds[0].CurrentSource.PinnedCommit != nil {
		t.Fatal("登记后的聚合投影不完整")
	}
	decode[project.Receipt](t, call(t, f.handler, "POST", url, command), 201, "Receipt")
	bad := project.LayoutCommand{MutationID: id(t), BaseRevision: "1", Positions: []project.NodePosition{}}
	decode[any](t, call(t, f.handler, "PUT", "/api/v1/projects/"+current.ID+"/layout", bad), 400, "ErrorResponse")
	if after := stateSnapshot(t, f, current.ID); after.Cursor != value.Cursor || !reflect.DeepEqual(after.Project, value.Project) {
		t.Fatal("重放或拒绝推进了项目通知")
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	opened := stateSnapshot(t, f, current.ID)
	if opened.Cursor != value.Cursor || !reflect.DeepEqual(opened.Project, registered.Project) || !reflect.DeepEqual(opened.Graph, value.Graph) {
		t.Fatal("控制面重开丢失一致状态")
	}
	page, err := f.database.ProjectEvents(context.Background(), current.ID, "0")
	if err != nil || len(page.Events) != 2 || page.Next != "2" || page.HasMore {
		t.Fatal("持久重放不完整", err)
	}
	assertStateEvents(t, page.Events, current.ID)
	if page.Events[0].Sequence != "1" || page.Events[1].Revision != "1" || page.Events[1].Reference != nil {
		t.Fatal("项目通知混淆了序号和revision")
	}
}

// TestProjectStateConcurrentBarrier 并发写入期间，游标与画布必须来自同一个RR快照。
func TestProjectStateConcurrentBarrier(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	failures := make(chan error, 4)
	var wait sync.WaitGroup
	wait.Go(func() {
		if err := advanceStateLayouts(context.Background(), f, current.ID); err != nil {
			failures <- err
		}
	})
	for range 3 {
		wait.Go(func() {
			if err := checkStateBarriers(context.Background(), f, current.ID); err != nil {
				failures <- err
			}
		})
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if value := stateSnapshot(t, f, current.ID); value.Project.Revision != "16" || value.Cursor != "17" {
		t.Fatal("并发读取改变了最终状态")
	}
}

// advanceStateLayouts 真实串行提交16个新意图，并把任何失败交还并发测试拥有者。
func advanceStateLayouts(ctx context.Context, f *fixture, id string) error {
	for number := range 16 {
		mutation, err := project.NewID()
		if err != nil {
			return err
		}
		command := project.LayoutCommand{MutationID: mutation, BaseRevision: strconv.Itoa(number), Positions: []project.NodePosition{}}
		if _, err := f.database.SaveLayout(ctx, id, command, [32]byte{}); err != nil {
			return err
		}
	}
	return nil
}

// checkStateBarriers 独立读者只接受来自同一RR快照的画布和连续序号。
func checkStateBarriers(ctx context.Context, f *fixture, id string) error {
	for range 24 {
		value, err := f.database.ProjectState(ctx, id)
		if err != nil {
			return err
		}
		revision, err := strconv.ParseUint(value.Project.Revision, 10, 64)
		if err != nil || value.Cursor != strconv.FormatUint(revision+1, 10) {
			return fmt.Errorf("画布与游标跨提交混合")
		}
	}
	return nil
}

// TestProjectStateNotificationRollback 通知落盘失败必须撤销项目、回执和序号。
func TestProjectStateNotificationRollback(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	db := stateSQL(t, f)
	constraint := "state_fail_" + strings.ReplaceAll(id(t), "-", "")
	if _, err := db.Exec("ALTER TABLE project_state_events ADD CONSTRAINT " + constraint + " CHECK(project_id <> '" + current.ID + "' OR sequence=1)"); err != nil {
		t.Fatal(err)
	}
	dropped := false
	t.Cleanup(func() {
		if !dropped {
			if _, err := db.Exec("ALTER TABLE project_state_events DROP CHECK " + constraint); err != nil {
				t.Error(err)
			}
		}
	})
	command := project.RegisterCommand{MutationID: id(t), BaseRevision: "0", RepositoryPath: repository(t, f.root, "rollback"), Position: project.Position{}}
	url := "/api/v1/projects/" + current.ID + "/services"
	decode[any](t, call(t, f.handler, "POST", url, command), 503, "ErrorResponse")
	if value := stateSnapshot(t, f, current.ID); value.Cursor != "1" || value.Project.Revision != "0" || len(value.Project.Nodes) != 0 {
		t.Fatal("失败通知留下部分声明或序号空洞")
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	if _, err := db.Exec("ALTER TABLE project_state_events DROP CHECK " + constraint); err != nil {
		t.Fatal(err)
	}
	dropped = true
	decode[project.Receipt](t, call(t, f.handler, "POST", url, command), 201, "Receipt")
	if value := stateSnapshot(t, f, current.ID); value.Cursor != "2" || value.Project.Revision != "1" {
		t.Fatal("恢复原请求未连续提交")
	}
}

// TestProjectStateObservationRefresh 真实SQL合成采样验证时间刷新，不将其冒称真实Docker资源。
func TestProjectStateObservationRefresh(t *testing.T) {
	f := openRuntimeFixture(t)
	receipt := f.accept(t, f.command)
	now := time.Now().UTC()
	fact := mysqlruntime.Observation{State: "running", Running: true, Ready: true, ServerUUID: id(t), ContainerID: strings.Repeat("a", 64), VolumeFound: true, ObservedAt: mysqlruntime.Timestamp(now)}
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", now); err != nil {
		t.Fatal(err)
	}
	before, err := f.base.database.ProjectState(context.Background(), f.current.ID)
	if err != nil {
		t.Fatal(err)
	}
	fact.ObservedAt = mysqlruntime.Timestamp(now.Add(time.Millisecond))
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	after, err := f.base.database.ProjectState(context.Background(), f.current.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.base.database.ProjectEvents(context.Background(), f.current.ID, before.Cursor)
	if err != nil || len(page.Events) != 1 || after.Runtime.Sequence != before.Runtime.Sequence || after.Cursor == before.Cursor || after.Runtime.Observation.ObservedAt != fact.ObservedAt {
		t.Fatal("正常采样刷新未通知前端", err)
	}
	assertStateEvents(t, page.Events, f.current.ID)
	if page.Events[0].Reference == nil || page.Events[0].Reference.OperationID != receipt.OperationID || page.Events[0].Scope != "runtime" {
		t.Fatal("通知缺少独立操作出处")
	}
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, nil, "DOCKER_UNAVAILABLE", now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	unknown, err := f.base.database.ProjectState(context.Background(), f.current.ID)
	if err != nil || unknown.Runtime.Observation == nil || unknown.Runtime.Freshness != "unknown" || unknown.Runtime.Observation.ServerUUID != fact.ServerUUID {
		t.Fatal("未知采样抹除最后事实或保持假健康", err)
	}
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", now); err != nil {
		t.Fatal(err)
	}
	late, err := f.base.database.ProjectState(context.Background(), f.current.ID)
	if err != nil || late.Cursor != unknown.Cursor {
		t.Fatal("迟到采样推进当前通知", err)
	}
}

// requireStateError 验证安全领域错误，未知数据库错误不能被伪装成窗口失效。
func requireStateError(t *testing.T, err error, code string) {
	t.Helper()
	var failure *project.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatal("状态错误类型不符", code, err)
	}
}
