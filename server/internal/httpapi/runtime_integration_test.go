//go:build integration

// 本文件用真实SQL验证受理、回滚、执行围栏与观测输入；真实Docker事实另由浏览器端到端验收。
package httpapi_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// runtimeFixture 只拥有本次新项目；它不启动后台执行器或向Docker注入测试事实。
type runtimeFixture struct {
	base      *fixture
	driver    *mysqlruntime.Driver
	sql       *sql.DB
	current   project.Project
	component project.MiddlewareNode
	command   mysqlruntime.Command
}

// openRuntimeFixture 复用门禁唯一的私有根，安装身份与真实服务一致。
func openRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	f := openFixture(t)
	directory := os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY")
	if directory == "" {
		t.Fatal("运行集成检查必须注入独立临时运行根")
	}
	driver, err := mysqlruntime.OpenDriver(directory, []string{f.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	attachRuntime(t, f, driver)
	current := create(t, f)
	command := middlewareCommand(t, f, current)
	command.Configuration = map[string]any{"runtimeProfile": "local-compose-v1"}
	saved := decode[project.Receipt](t, call(t, f.handler, "POST", canvasURL(current)+"/middlewares", command), 201, "CanvasReceipt")
	database, err := sql.Open("mysql", f.dsn)
	if err != nil {
		t.Fatal("临时运行验证连接不可用")
	}
	value := &runtimeFixture{base: f, driver: driver, sql: database, current: saved.Project, component: saved.Canvas.Middlewares[0], command: mysqlruntime.Command{MutationID: id(t), BaseRevision: saved.Project.Revision, BaseGeneration: "0", Desired: "running"}}
	t.Cleanup(func() {
		_, err := database.Exec("UPDATE runtime_operations SET state='failed',lease_until=NULL,code='TEST_FINISHED' WHERE project_id=? AND state IN ('accepted','executing','waiting_retry')", value.current.ID)
		if err := errors.Join(err, database.Close()); err != nil {
			t.Error(err)
		}
	})
	return value
}

// attachRuntime 装配真实同库用例，不用内存副本替代持久化与HTTP契约。
func attachRuntime(t *testing.T, f *fixture, driver *mysqlruntime.Driver) {
	t.Helper()
	if driver != nil {
		if err := f.database.BindRuntime(context.Background(), driver.InstallationID(), driver.Environment().DaemonID); err != nil {
			t.Fatal(err)
		}
	}
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := gitworkspace.New([]string{f.root})
	if err != nil {
		t.Fatal(err)
	}
	service := project.NewService(f.database, f.database, f.database, inspector, definitions)
	running := mysqlruntime.NewService(f.database, f.database, f.database, definitions, driver)
	f.handler, err = httpapi.New(service, host, []string{"http://" + host}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Features{Runtime: running})
	if err != nil {
		t.Fatal(err)
	}
}

// targetURL 固定本次项目与组件的受理路由。
func (f *runtimeFixture) targetURL() string {
	return "/api/v1/projects/" + f.current.ID + "/runtime/" + f.component.ID + "/targets"
}

// snapshot 通过发布HTTP契约读取同库运行快照。
func (f *runtimeFixture) snapshot(t *testing.T) mysqlruntime.Snapshot {
	t.Helper()
	return decode[mysqlruntime.Snapshot](t, call(t, f.base.handler, "GET", "/api/v1/projects/"+f.current.ID+"/runtime", nil), 200, "RuntimeSnapshot")
}

// accept 只验证受理，不将测试输入说成Docker已运行。
func (f *runtimeFixture) accept(t *testing.T, command mysqlruntime.Command) mysqlruntime.Receipt {
	t.Helper()
	return decode[mysqlruntime.Receipt](t, call(t, f.base.handler, "POST", f.targetURL(), command), 202, "RuntimeReceipt")
}

// claim 验证队列领取的是本次仍有效的原目标。
func (f *runtimeFixture) claim(t *testing.T) *mysqlruntime.Lease {
	t.Helper()
	now := time.Now().UTC()
	lease, err := f.base.database.ClaimRuntime(context.Background(), now, now.Add(3*time.Minute))
	if err != nil || lease == nil || lease.Target.ProjectID != f.current.ID {
		t.Fatal("未领取本次有效运行目标")
	}
	return lease
}

// TestRuntimeAcceptanceAndOriginalRecovery 画布revision、运行generation和原键回执分别持久化。
func TestRuntimeAcceptanceAndOriginalRecovery(t *testing.T) {
	f := openRuntimeFixture(t)
	if value := f.snapshot(t); value.Generation != "0" || value.Target != nil {
		t.Fatal("未受理项目出现伪造目标")
	}
	accepted := f.accept(t, f.command)
	if accepted.Target.CanvasRevision != f.current.Revision {
		t.Fatal("运行目标缺少原画布revision出处")
	}
	value := f.snapshot(t)
	if value.Generation != "1" || value.Operation.MutationID != f.command.MutationID || value.Operation.State != "accepted" || len(value.Operation.Steps) != 6 {
		t.Fatal("目标或步骤未同时受理")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY"), "projects", accepted.Target.Resource.ID)); !os.IsNotExist(err) {
		t.Fatal("HTTP受理阶段执行了私有材料或外部动作")
	}
	replayed := f.accept(t, f.command)
	if !replayed.Replayed || replayed.OperationID != accepted.OperationID || !reflect.DeepEqual(replayed.Target, accepted.Target) {
		t.Fatal("同请求没有返回原目标")
	}
	conflict := f.command
	conflict.Desired = "stopped"
	decode[any](t, call(t, f.base.handler, "POST", f.targetURL(), conflict), 409, "ErrorResponse")
	if err := f.base.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.base.reopen(t)
	attachRuntime(t, f.base, f.driver)
	opened := f.snapshot(t)
	if opened.Target.ID != accepted.Target.ID || opened.Operation.ID != accepted.OperationID {
		t.Fatal("重开后丢失目标与操作身份")
	}
	receipt := decode[mysqlruntime.Receipt](t, call(t, f.base.handler, "GET", "/api/v1/runtime-mutations/"+f.command.MutationID, nil), 200, "RuntimeReceipt")
	if receipt.OperationID != accepted.OperationID {
		t.Fatal("原回执未恢复")
	}
	canvas := decode[project.CanvasSnapshot](t, call(t, f.base.handler, "GET", canvasURL(f.current), nil), 200, "CanvasSnapshot")
	if canvas.Project.Revision != f.current.Revision {
		t.Fatal("运行目标错误推进画布revision")
	}
}

// TestRuntimeOldExecutionCannotPublishNewTarget 旧步骤结果可保留，但不得覆盖新停止目标。
func TestRuntimeOldExecutionCannotPublishNewTarget(t *testing.T) {
	f := openRuntimeFixture(t)
	accepted := f.accept(t, f.command)
	old := f.claim(t)
	valid, err := f.base.database.CheckpointRuntime(context.Background(), *old, mysqlruntime.Checkpoint{Name: "preflight", State: "executing"})
	if err != nil || !valid {
		t.Fatal("当前执行无法登记步骤")
	}
	stop := f.command
	stop.MutationID = id(t)
	stop.BaseGeneration = "1"
	stop.Desired = "stopped"
	stop.RetryOf = accepted.OperationID
	latest := f.accept(t, stop)
	valid, err = f.base.database.CheckpointRuntime(context.Background(), *old, mysqlruntime.Checkpoint{Name: "preflight", State: "succeeded", ExternalID: old.Target.Resource.DaemonID})
	if err != nil || valid {
		t.Fatal("迟到旧结果获得当前执行权限")
	}
	if err := f.base.database.FinishRuntime(context.Background(), *old, mysqlruntime.Completion{State: "succeeded", NextAttempt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	if value.Target.Generation != "2" || value.Operation.ID != latest.OperationID || value.Operation.State != "accepted" || value.Observation != nil {
		t.Fatal("旧执行器覆盖了新目标")
	}
	original := decode[mysqlruntime.Operation](t, call(t, f.base.handler, "GET", "/api/v1/projects/"+f.current.ID+"/runtime/operations/"+accepted.OperationID, nil), 200, "RuntimeOperation")
	if original.State != "superseded" {
		t.Fatal("原操作替代原因丢失")
	}
	next := f.claim(t)
	if next.Operation.ID != latest.OperationID || next.Target.Resource.ID != old.Target.Resource.ID {
		t.Fatal("停止目标创建了另一数据身份")
	}
}

// TestRuntimeSQLFailureRollsBackWholeAdmission 真实约束失败不得留下半个目标、步骤、事件或回执。
func TestRuntimeSQLFailureRollsBackWholeAdmission(t *testing.T) {
	f := openRuntimeFixture(t)
	name := "runtime_fail_" + strings.ReplaceAll(id(t), "-", "")
	if _, err := f.sql.Exec("ALTER TABLE runtime_events ADD CONSTRAINT " + name + " CHECK(project_id <> '" + f.current.ID + "')"); err != nil {
		t.Fatal(err)
	}
	dropped := false
	t.Cleanup(func() {
		if !dropped {
			if _, err := f.sql.Exec("ALTER TABLE runtime_events DROP CHECK " + name); err != nil {
				t.Error(err)
			}
		}
	})
	decode[any](t, call(t, f.base.handler, "POST", f.targetURL(), f.command), 503, "ErrorResponse")
	if value := f.snapshot(t); value.Generation != "0" || value.Target != nil || value.Operation != nil {
		t.Fatal("失败写入留下了部分运行目标")
	}
	decode[any](t, call(t, f.base.handler, "GET", "/api/v1/runtime-mutations/"+f.command.MutationID, nil), 404, "ErrorResponse")
	if _, err := f.sql.Exec("ALTER TABLE runtime_events DROP CHECK " + name); err != nil {
		t.Fatal(err)
	}
	dropped = true
	accepted := f.accept(t, f.command)
	if accepted.Target.Generation != "1" || accepted.Replayed {
		t.Fatal("原请求恢复没有从完整未受理状态开始")
	}
}

// TestRuntimeExpiredLeaseAndCheckpointRollback 新执行代次恢复原目标，旧租约和失败检查点都不能推进事实。
func TestRuntimeExpiredLeaseAndCheckpointRollback(t *testing.T) {
	f := openRuntimeFixture(t)
	f.accept(t, f.command)
	old := f.claim(t)
	if _, err := f.sql.Exec("UPDATE runtime_operations SET lease_until=? WHERE id=?", time.Now().UTC().Add(-time.Second), old.Operation.ID); err != nil {
		t.Fatal(err)
	}
	next := f.claim(t)
	if next.Execution == old.Execution || next.Target.ID != old.Target.ID || next.Operation.Attempts != 2 {
		t.Fatal("过期恢复换了目标或未提升执行代次")
	}
	valid, err := f.base.database.CheckpointRuntime(context.Background(), *old, mysqlruntime.Checkpoint{Name: "preflight", State: "executing"})
	if err != nil || valid {
		t.Fatal("旧执行代次继续获准写入")
	}
	before := f.snapshot(t)
	if _, err := f.base.database.CheckpointRuntime(context.Background(), *next, mysqlruntime.Checkpoint{Name: "preflight", State: "invalid"}); err == nil {
		t.Fatal("真实SQL状态约束没有拒绝非法检查点")
	}
	after := f.snapshot(t)
	if after.Sequence != before.Sequence || after.Operation.Version != before.Operation.Version {
		t.Fatal("失败检查点部分推进操作或事件版本")
	}
}

// TestRuntimeObservationOrdering 保存较新事实，Docker未知仍保留最后事实而不继续显示绿色。
func TestRuntimeObservationOrdering(t *testing.T) {
	f := openRuntimeFixture(t)
	receipt := f.accept(t, f.command)
	base := time.Now().UTC().Add(-3 * time.Second).Truncate(time.Second)
	fact := mysqlruntime.Observation{State: "running", Running: true, Ready: true, ServerUUID: id(t), ContainerID: strings.Repeat("a", 64), VolumeFound: true, ObservedAt: mysqlruntime.Timestamp(base)}
	now := time.Now().UTC()
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", now); err != nil {
		t.Fatal(err)
	}
	newer := fact
	newer.ObservedAt = mysqlruntime.Timestamp(base.Add(500 * time.Millisecond))
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &newer, "", now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if value := f.snapshot(t); value.Observation.ObservedAt != newer.ObservedAt {
		t.Fatal("不等长RFC3339小数的字典排序覆盖了较新事实")
	}
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, nil, "DOCKER_UNAVAILABLE", now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	if value.Freshness != "unknown" || value.Observation.ServerUUID != fact.ServerUUID || value.ObservationCode != "DOCKER_UNAVAILABLE" {
		t.Fatal("采集失败抹掉事实或假报实时健康")
	}
}

// TestRuntimeDatabaseIdentityChangeRemainsVisible 原数据身份不随外部替换悄悄漂移。
func TestRuntimeDatabaseIdentityChangeRemainsVisible(t *testing.T) {
	f := openRuntimeFixture(t)
	receipt := f.accept(t, f.command)
	fact := mysqlruntime.Observation{State: "running", Running: true, Ready: true, ServerUUID: id(t), ContainerID: strings.Repeat("a", 64), VolumeFound: true, ObservedAt: mysqlruntime.Timestamp(time.Now())}
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	fact.ServerUUID = id(t)
	fact.ObservedAt = mysqlruntime.Timestamp(time.Now())
	if err := f.base.database.ObserveRuntime(context.Background(), receipt.Target, &fact, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	if value.Observation.Ready || value.Observation.State != "drifted" || value.ObservationCode != "RUNTIME_DATABASE_IDENTITY_CONFLICT" {
		t.Fatal("外部数据库身份替换被当作原库成功")
	}
}
