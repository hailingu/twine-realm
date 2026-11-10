//go:build integration

// 本文件构造独立已提交业务仓库，使用真实受理、构建、MySQL与Compose证明部署。
package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicecompose"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentFixture 只拥有随机测试项目，所有真实副作用都来自已接受目标。
type deploymentFixture struct {
	runtime    *runtimeFixture
	build      *servicebuild.Driver
	adapter    *servicecompose.Driver
	service    *servicedeploy.Service
	project    project.Project
	graph      project.GraphState
	repository string
	candidate  servicebuild.Candidate
	request    servicedeploy.PreviewRequest
}

// openDeploymentFixture 在平台门禁新根导出独立Todo示例，不改动已发布工程包材料。
func openDeploymentFixture(t *testing.T) *deploymentFixture {
	return openDeploymentScenario(t, false)
}

// openDeploymentScenario 失败用例也先真实检查并构建，不能直接伪造可部署候选。
func openDeploymentScenario(t *testing.T, migrationFailure bool) *deploymentFixture {
	t.Helper()
	running := openRuntimeFixture(t)
	repository := deploymentRepository(t, running.base.root, migrationFailure)
	registered := decode[project.Receipt](t, call(t, running.base.handler, "POST", "/api/v1/projects/"+running.current.ID+"/services", project.RegisterCommand{MutationID: id(t), BaseRevision: running.current.Revision, RepositoryPath: repository, DisplayName: "独立Todo示例", Position: project.Position{X: 10, Y: 10}}), 201, "Receipt")
	pinned := decode[project.Receipt](t, call(t, running.base.handler, "PUT", sourceURL(registered.Project), project.PinSourceCommand{MutationID: id(t), BaseRevision: registered.Project.Revision, Commit: sourceGit(t, repository, "rev-parse", "HEAD")}), 200, "SourceReceipt")
	graph := project.GraphSnapshot{Project: pinned.Project, Graph: project.GraphState{Middlewares: []project.MiddlewareNode{running.component}}}
	saved := editGraph(t, running.base, pinned.Project, contractOperation(graph, contractDocument(t)), bindingOperation(graph, "todo_example"))
	driver, err := servicebuild.OpenDriver(os.Getenv("TWINE_TEST_BUILD_DIRECTORY"), []string{running.base.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	digest, err := servicebuild.Fingerprint(driver.Environment())
	if err != nil {
		t.Fatal(err)
	}
	if err := running.base.database.BindBuild(context.Background(), driver.InstallationID(), digest); err != nil {
		t.Fatal(err)
	}
	adapter, err := servicecompose.NewDriver(running.base.database, running.driver, driver)
	if err != nil {
		t.Fatal(err)
	}
	value := &deploymentFixture{runtime: running, build: driver, adapter: adapter, project: saved.Project, graph: *saved.Graph, repository: repository}
	value.attach(t)
	value.startDatabase(t)
	value.buildCandidate(t)
	value.request = servicedeploy.PreviewRequest{BaseRevision: saved.Project.Revision, BaseGeneration: "0", CandidateID: value.candidate.ID, BindingID: value.graph.Bindings[0].ID, HostPort: deploymentPort(t), Configuration: map[string]any{}}
	return value
}

// deploymentRepository 先导出原工程，再添加测试业务源码，最后固定Git提交。
func deploymentRepository(t *testing.T, root string, migrationFailure bool) string {
	t.Helper()
	profile, err := engineering.Load()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(root, "todo-service")
	if _, err := profile.Export(repository); err != nil {
		t.Fatal(err)
	}
	writeDeploymentBusinessFiles(t, repository, migrationFailure)
	main := filepath.Join(repository, "cmd/service/main.go")
	data, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "app.New(configuration.Name, backend)", "app.WithTodo(app.New(configuration.Name, backend), db)", 1)
	if updated == string(data) {
		t.Fatal("业务入口未按已选模板接入")
	}
	if err := os.WriteFile(main, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	format := exec.Command("gofmt", "-w", repository)
	if output, err := format.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatal("示例Go格式准备未通过")
	}
	sourceGit(t, repository, "init", "--initial-branch=main")
	sourceGit(t, repository, "-c", "core.hooksPath=/dev/null", "add", ".")
	sourceGit(t, repository, "-c", "core.hooksPath=/dev/null", "-c", "user.name=部署验收", "-c", "user.email=deploy@example.invalid", "commit", "-m", "固定独立Todo示例")
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

// writeDeploymentBusinessFiles 业务夹具与迁移失败材料只写入本测试导出的独立仓库。
func writeDeploymentBusinessFiles(t *testing.T, repository string, migrationFailure bool) {
	t.Helper()
	for _, name := range []string{"todo.go", "000002_todos.up.sql", "000002_todos.down.sql"} {
		data, err := os.ReadFile(filepath.Join("testdata/deployment", name))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(repository, "internal/app", name)
		if name != "todo.go" {
			destination = filepath.Join(repository, "internal/schema/migrations", name)
		}
		if migrationFailure && name == "000002_todos.up.sql" {
			data = append(data, []byte("\nALTER TABLE twine_missing_table ADD COLUMN name VARCHAR(10);\n")...)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// attach 重新装配真实服务，恢复不会依赖旧进程内存。
func (f *deploymentFixture) attach(t *testing.T) {
	t.Helper()
	base := f.runtime.base
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := gitworkspace.New([]string{base.root})
	if err != nil {
		t.Fatal(err)
	}
	projectService := project.NewService(base.database, base.database, base.database, inspector, definitions)
	runtimeService := mysqlruntime.NewService(base.database, base.database, base.database, definitions, f.runtime.driver)
	buildService, err := servicebuild.NewService(base.database, base.database, base.database, inspector, f.build)
	if err != nil {
		t.Fatal(err)
	}
	f.service = servicedeploy.NewService(base.database, base.database, f.adapter)
	base.handler, err = httpapi.New(projectService, host, []string{"http://" + host}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Features{Runtime: runtimeService, Build: buildService, Deploy: f.service})
	if err != nil {
		t.Fatal(err)
	}
}

// startDatabase 必须真实认证原MySQL，再将实际结果结清同库操作。
func (f *deploymentFixture) startDatabase(t *testing.T) {
	t.Helper()
	f.runtime.current = f.project
	f.runtime.command.BaseRevision = f.project.Revision
	f.runtime.accept(t, f.runtime.command)
	lease := f.runtime.claim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	fact, err := f.runtime.driver.Execute(ctx, *lease, 120*time.Second, func(ctx context.Context, point mysqlruntime.Checkpoint) (bool, error) {
		return f.runtime.base.database.CheckpointRuntime(ctx, *lease, point)
	})
	if err != nil {
		t.Fatal("部署前真实MySQL未就绪", err)
	}
	if err := f.runtime.base.database.FinishRuntime(ctx, *lease, mysqlruntime.Completion{State: "succeeded", Code: "READY", Message: "真实MySQL已认证", NextAttempt: time.Now().UTC(), Observation: fact}); err != nil {
		t.Fatal(err)
	}
}

// buildCandidate 走真实固定commit、离线检查、静态编译与本地镜像路径，不伪造候选。
func (f *deploymentFixture) buildCandidate(t *testing.T) {
	t.Helper()
	base := f.runtime.base
	inspector, err := gitworkspace.New([]string{base.root})
	if err != nil {
		t.Fatal(err)
	}
	service, err := servicebuild.NewService(base.database, base.database, base.database, inspector, f.build)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := engineering.Load()
	if err != nil {
		t.Fatal(err)
	}
	source, err := base.database.Source(context.Background(), f.project.ID, f.project.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	_, err = service.Accept(ctx, f.project.ID, f.project.Nodes[0].ID, servicebuild.Command{MutationID: id(t), BaseRevision: f.project.Revision, BaseGeneration: "0", Commit: *source.PinnedCommit, Profile: profile.Identity()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease, err := base.database.ClaimBuild(ctx, now, now.Add(9*time.Minute))
	if err != nil || lease == nil || lease.Target.ProjectID != f.project.ID {
		t.Fatal("未领取本次真实构建", err)
	}
	candidate, err := f.executeFixtureBuild(ctx, lease.Target, lease.Operation.ID, inspector, func(ctx context.Context, point servicebuild.Step) (bool, error) {
		t.Log("部署示例构建", point.Name, point.State)
		return base.database.CheckpointBuild(ctx, *lease, point)
	}, t)
	if err != nil {
		t.Fatal("独立业务示例真实构建失败", err)
	}
	if err := base.database.FinishBuild(ctx, *lease, servicebuild.Completion{State: "succeeded", Code: "CHECKED", Message: "真实业务示例构建通过", Candidate: candidate}); err != nil {
		t.Fatal(err)
	}
	f.candidate = *candidate
	f.refreshDatabase(t)
}

// executeFixtureBuild 只在门禁明确提供本轮私有缓存时复用固定夹具；生产构建API及专门真实构建验收不走此入口。
func (f *deploymentFixture) executeFixtureBuild(ctx context.Context, target servicebuild.Target, operationID string, exporter servicebuild.SourceExporter, report servicebuild.Reporter, t *testing.T) (candidate *servicebuild.Candidate, err error) {
	t.Helper()
	directory, runID := os.Getenv("TWINE_TEST_FIXTURE_CACHE_DIRECTORY"), os.Getenv("TWINE_TEST_FIXTURE_CACHE_RUN_ID")
	if directory == "" && runID == "" {
		return f.build.Execute(ctx, target, operationID, exporter, report)
	}
	cache, err := servicebuild.OpenFixtureCache(directory, runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := cache.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	candidate, hit, err := cache.Execute(ctx, f.build, target, operationID, exporter, report)
	if err != nil {
		return nil, err
	}
	if err := f.build.VerifyCandidate(ctx, *candidate); err != nil {
		return nil, err
	}
	mode := "cold"
	if hit {
		mode = "hit"
	}
	t.Log("[FIXTURE_BUILD_CACHE]", mode, "operation", candidate.OperationID, "source", candidate.SourceDigest, "binary", candidate.Image.BinaryDigest, "process", os.Getpid())
	for _, check := range candidate.Checks {
		if check.Name == "vet" || check.Name == "test" || check.Name == "build" {
			t.Log("[FIXTURE_BUILD_PROOF]", mode, check.Name, "originalCompletedAt", check.CompletedAt, "output", check.OutputDigest)
		}
	}
	return candidate, nil
}

// refreshDatabase 构建耗时后重新取得真实MySQL事实，不篡改时间伪造新鲜度。
func (f *deploymentFixture) refreshDatabase(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snapshot, err := f.runtime.base.database.RuntimeSnapshot(ctx, f.project.ID)
	if err != nil || snapshot.Target == nil {
		t.Fatal("原MySQL目标缺失", err)
	}
	fact, err := f.runtime.driver.Inspect(ctx, snapshot.Target.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.base.database.ObserveRuntime(ctx, captureRuntimeTarget(t, f.runtime.base.database, *snapshot.Target), fact, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// deploymentPort 仅选择本机临时业务端口，不改动用户监听进程。
func deploymentPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	closeErr := listener.Close()
	if err != nil || closeErr != nil {
		t.Fatal("测试端口未核实释放")
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
