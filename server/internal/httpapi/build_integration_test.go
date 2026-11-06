//go:build integration

// 本文件穿过真实MySQL、Git和HTTP契约核实构建受理、原键恢复、取消与执行围栏。
package httpapi_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// buildFixture 仅拥有本次随机项目，不启动后台执行或接触用户仓库。
type buildFixture struct {
	base    *fixture
	driver  *servicebuild.Driver
	sql     *sql.DB
	current project.Project
	command servicebuild.Command
}

// openBuildFixture 使用门禁独立构建根并创建真实已固定源码关联。
func openBuildFixture(t *testing.T) *buildFixture {
	t.Helper()
	f := openFixture(t)
	path := os.Getenv("TWINE_TEST_BUILD_DIRECTORY")
	if path == "" {
		t.Fatal("构建集成检查必须由门禁注入独立根")
	}
	driver, err := servicebuild.OpenDriver(path, []string{f.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	attachBuild(t, f, driver)
	registered, _ := registerSource(t, f, true)
	current := registered.Project
	commit := *current.Nodes[0].Repository.HeadCommit
	pinned := decode[project.Receipt](t, call(t, f.handler, "PUT", sourceURL(current), project.PinSourceCommand{MutationID: id(t), BaseRevision: current.Revision, Commit: commit}), 200, "SourceReceipt")
	catalog, err := engineering.Load()
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("mysql", f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	value := &buildFixture{f, driver, database, pinned.Project, servicebuild.Command{MutationID: id(t), BaseRevision: pinned.Project.Revision, BaseGeneration: "0", Commit: commit, Profile: catalog.Identity()}}
	t.Cleanup(func() {
		_, err := database.Exec("UPDATE build_operations SET state='failed',lease_until=NULL,payload=JSON_SET(payload,'$.state','failed','$.code','TEST_FINISHED') WHERE project_id=? AND state IN ('accepted','executing','cancelling')", value.current.ID)
		if err := errors.Join(err, database.Close()); err != nil {
			t.Error(err)
		}
	})
	return value
}

// attachBuild 每次重开都重新装配真实用例与数据库，原结果不依赖旧进程内存。
func attachBuild(t *testing.T, f *fixture, driver *servicebuild.Driver) {
	t.Helper()
	if driver != nil {
		digest, err := servicebuild.Fingerprint(driver.Environment())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.database.BindBuild(context.Background(), driver.InstallationID(), digest); err != nil {
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
	building, err := servicebuild.NewService(f.database, f.database, f.database, inspector, driver)
	if err != nil {
		t.Fatal(err)
	}
	service := project.NewService(f.database, f.database, f.database, inspector, definitions)
	f.handler, err = httpapi.New(service, host, []string{"http://" + host}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Features{Build: building})
	if err != nil {
		t.Fatal(err)
	}
}

// route 固定此项目和服务，不接受其他会话的操作身份。
func (f *buildFixture) route() string {
	return "/api/v3/projects/" + f.current.ID + "/services/" + f.current.Nodes[0].ID
}

// accept 验证真实202与发布回执，不将其作为镜像成功证据。
func (f *buildFixture) accept(t *testing.T, command servicebuild.Command) servicebuild.Receipt {
	t.Helper()
	return decode[servicebuild.Receipt](t, call(t, f.base.handler, "POST", f.route()+"/builds", command), 202, "BuildReceipt")
}

// snapshot 核实完整schema和当前指针，读取不发起新的构建。
func (f *buildFixture) snapshot(t *testing.T) servicebuild.Snapshot {
	t.Helper()
	return decode[servicebuild.Snapshot](t, call(t, f.base.handler, "GET", f.route()+"/build", nil), 200, "BuildSnapshot")
}

// TestBuildAdmissionAndOriginalReceipt 验证同键并发只受理一次，重启和禁用执行器仍可核实原键。
func TestBuildAdmissionAndOriginalReceipt(t *testing.T) {
	f := openBuildFixture(t)
	capability := decode[servicebuild.Capability](t, call(t, f.base.handler, "GET", "/api/v3/build/capabilities", nil), 200, "BuildCapability")
	if !capability.Enabled || f.snapshot(t).Generation != "0" {
		t.Fatal("构建初始状态不明确")
	}
	var wait sync.WaitGroup
	values := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wait.Go(func() { values <- call(t, f.base.handler, "POST", f.route()+"/builds", f.command) })
	}
	wait.Wait()
	close(values)
	var operationID string
	for response := range values {
		value := decode[servicebuild.Receipt](t, response, 202, "BuildReceipt")
		if operationID != "" && operationID != value.OperationID {
			t.Fatal("同键并发产生多个构建")
		}
		operationID = value.OperationID
	}
	value := f.snapshot(t)
	if value.Operation == nil || value.Operation.ID != operationID || value.Operation.State != "accepted" || value.Candidate != nil || value.CandidateCurrent {
		t.Fatal("受理被误报为构建成功")
	}
	changed := f.command
	changed.Commit = string(make([]byte, 40))
	buildFailure(t, call(t, f.base.handler, "POST", f.route()+"/builds", changed), 400, "INVALID_REQUEST")
	changed = f.command
	changed.BaseGeneration = "1"
	buildFailure(t, call(t, f.base.handler, "POST", f.route()+"/builds", changed), 409, "IDEMPOTENCY_CONFLICT")
	changed.MutationID = id(t)
	buildFailure(t, call(t, f.base.handler, "POST", f.route()+"/builds", changed), 409, "BUILD_BUSY")
	if err := f.base.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.base.reopen(t)
	attachBuild(t, f.base, nil)
	receipt := decode[servicebuild.Receipt](t, call(t, f.base.handler, "GET", "/api/v3/build-mutations/"+f.command.MutationID, nil), 200, "BuildReceipt")
	if receipt.OperationID != operationID || !receipt.Replayed || f.accept(t, f.command).OperationID != operationID {
		t.Fatal("重开或禁用导致重复受理")
	}
	changed = f.command
	changed.MutationID = id(t)
	buildFailure(t, call(t, f.base.handler, "POST", f.route()+"/builds", changed), 503, "BUILD_DISABLED")
}

// claimBuild 领取本次仍被当前服务拥有的执行，不能越过其他测试操作。
func (f *buildFixture) claimBuild(t *testing.T) *servicebuild.Lease {
	t.Helper()
	now := time.Now().UTC()
	lease, err := f.base.database.ClaimBuild(context.Background(), now, now.Add(9*time.Minute))
	if err != nil || lease == nil || lease.Target.ProjectID != f.current.ID {
		t.Fatal("构建租约未属于此测试：", err)
	}
	return lease
}

// TestBuildCancellationAndFences 取消先提交、旧检查点被拒绝、终态不带候选，随后才可明确重新构建。
func TestBuildCancellationAndFences(t *testing.T) {
	f := openBuildFixture(t)
	receipt := f.accept(t, f.command)
	lease := f.claimBuild(t)
	point := servicebuild.Step{Name: "source", State: "executing", UpdatedAt: servicebuild.Timestamp(time.Now())}
	if valid, err := f.base.database.CheckpointBuild(context.Background(), *lease, point); err != nil || !valid {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	command := servicebuild.CancelCommand{MutationID: id(t), BaseVersion: value.Operation.Version}
	url := f.route() + "/builds/" + receipt.OperationID + "/cancel"
	cancelled := decode[servicebuild.Receipt](t, call(t, f.base.handler, "POST", url, command), 202, "BuildReceipt")
	if cancelled.Kind != "cancelBuild" || cancelled.Version == command.BaseVersion {
		t.Fatal("取消未独立推进操作版本")
	}
	if authorized, err := f.base.database.BuildAuthorized(context.Background(), *lease); err != nil || authorized {
		t.Fatal("取消后仍授权长命令")
	}
	if valid, err := f.base.database.CheckpointBuild(context.Background(), *lease, point); err != nil || valid {
		t.Fatal("取消后旧执行继续写入检查点")
	}
	if err := f.base.database.FinishBuild(context.Background(), *lease, servicebuild.Completion{State: "failed", Code: "INTERRUPTED", Message: "受控取消结束"}); err != nil {
		t.Fatal(err)
	}
	value = f.snapshot(t)
	if value.Operation.State != "cancelled" || value.Candidate != nil {
		t.Fatal("取消后发布了候选")
	}
	replayed := decode[servicebuild.Receipt](t, call(t, f.base.handler, "POST", url, command), 202, "BuildReceipt")
	if !replayed.Replayed || replayed.Version != cancelled.Version {
		t.Fatal("原取消回执被后来终态覆盖")
	}
	assertNewBuildFencesOld(t, f, receipt, lease)
}

// TestBuildExpiredLeaseRecovery 真实过期租约只进入恢复，旧代次和损坏载荷都不能提交。
func TestBuildExpiredLeaseRecovery(t *testing.T) {
	f := openBuildFixture(t)
	f.accept(t, f.command)
	old := f.claimBuild(t)
	if _, err := f.sql.Exec("UPDATE build_operations SET lease_until=? WHERE id=?", time.Now().Add(-time.Second), old.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if valid, err := f.base.database.CheckpointBuild(context.Background(), *old, servicebuild.Step{Name: "vet", State: "succeeded"}); err != nil || valid {
		t.Fatal("过期围栏被放行")
	}
	recovered := f.claimBuild(t)
	if !recovered.Recover || recovered.Execution <= old.Execution {
		t.Fatal("重启恢复盲重跑了原业务代码")
	}
	if err := f.base.database.FinishBuild(context.Background(), *old, servicebuild.Completion{State: "failed", Code: "LATE_RESULT", Message: "迟到旧结果"}); err != nil {
		t.Fatal(err)
	}
	if err := f.base.database.FinishBuild(context.Background(), *recovered, servicebuild.Completion{State: "needs_attention", Code: "BUILD_INTERRUPTED", Message: "保留未知原始结果"}); err != nil {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	if value.Operation.State != "needs_attention" || value.Candidate != nil {
		t.Fatal("恢复猜测成功")
	}
	next := f.command
	next.MutationID, next.BaseGeneration, next.RetryOf = id(t), "1", old.Operation.ID
	buildFailure(t, call(t, f.base.handler, "POST", f.route()+"/builds", next), 409, "BUILD_RECOVERY_REQUIRED")
	if _, err := f.sql.Exec("UPDATE build_targets SET payload=JSON_SET(payload,'$.inputDigest',?) WHERE id=?", "sha256:"+strconv.FormatUint(0, 10), old.Target.ID); err != nil {
		t.Fatal(err)
	}
	buildFailure(t, call(t, f.base.handler, "GET", f.route()+"/build", nil), 503, "STORAGE_UNAVAILABLE")
	if reflect.DeepEqual(old, recovered) {
		t.Fatal("恢复租约没有独立身份")
	}
}

// buildFailure 对错误响应执行同一发布契约，并核实稳定错误码。
func buildFailure(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	value := decode[struct {
		Error *project.Error `json:"error"`
	}](t, response, status, "ErrorResponse")
	if value.Error == nil || value.Error.Code != code {
		t.Fatalf("期望%s，实际%v", code, value.Error)
	}
}

// assertNewBuildFencesOld 验收明确新代次后原执行无法覆盖当前指针。
func assertNewBuildFencesOld(t *testing.T, f *buildFixture, receipt servicebuild.Receipt, lease *servicebuild.Lease) {
	t.Helper()
	next := f.command
	next.MutationID, next.BaseGeneration, next.RetryOf = id(t), "1", receipt.OperationID
	accepted := f.accept(t, next)
	if accepted.Target.Generation != "2" || accepted.OperationID == receipt.OperationID {
		t.Fatal("明确新构建未建立新代次")
	}
	if err := f.base.database.FinishBuild(context.Background(), *lease, servicebuild.Completion{State: "failed", Code: "LATE_RESULT", Message: "迟到旧执行"}); err != nil {
		t.Fatal(err)
	}
	if f.snapshot(t).Operation.ID != accepted.OperationID {
		t.Fatal("旧结果覆盖新构建")
	}
}
