// 本文件验证预览读取失败不能变成受理，禁用执行器仍保留历史读取的错误语义。
package servicedeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// previewDeclarations 仅控制读取结果，不模拟Docker或生成持久状态。
type previewDeclarations struct {
	graph project.GraphSnapshot
	build servicebuild.Snapshot
	stage string
	err   error
}

// Graph 返回本场景的声明读取，不让读取失败伪装成空画布。
func (reader previewDeclarations) Graph(context.Context, string) (project.GraphSnapshot, error) {
	if reader.stage == "graph" {
		return project.GraphSnapshot{}, reader.err
	}
	return reader.graph, nil
}

// BuildSnapshot 保留固定候选读取失败，不制造替代候选。
func (reader previewDeclarations) BuildSnapshot(context.Context, string, string) (servicebuild.Snapshot, error) {
	if reader.stage == "build" {
		return servicebuild.Snapshot{}, reader.err
	}
	return reader.build, nil
}

// RuntimeSnapshot 只供预览用例的错误传播，真实数据库身份由集成测试验证。
func (reader previewDeclarations) RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error) {
	if reader.stage == "runtime" {
		return mysqlruntime.Snapshot{}, reader.err
	}
	return mysqlruntime.Snapshot{}, nil
}

// previewAdapter 只实现预览的读端口，意外执行部署或采集会使测试失败。
type previewAdapter struct {
	Adapter
	plan Plan
	err  error
}

// Prepare 返回结构夹具或明确错误，不声称此处进行了真实外部预检。
func (adapter previewAdapter) Prepare(context.Context, mysqlruntime.Snapshot, servicebuild.Snapshot) (Database, Environment, error) {
	return adapter.plan.Database, adapter.plan.Environment, adapter.err
}

// DeploymentSnapshot 受控读取失败不能变成默认零代次成功。
func (repository receiptRepository) DeploymentSnapshot(context.Context, string, string) (Snapshot, error) {
	return Snapshot{}, repository.err
}

// DeploymentOperation 原操作查询保持存储错误，不用新的操作补偿读取失败。
func (repository receiptRepository) DeploymentOperation(context.Context, string, string, string) (Operation, error) {
	return Operation{}, repository.err
}

// TestDeploymentPreviewReadFailures 各读边界失败与预览摘要冲突都在接受目标之前结束。
func TestDeploymentPreviewReadFailures(t *testing.T) {
	for _, stage := range []string{"graph", "build", "runtime", "preflight", "digest"} {
		t.Run(stage, func(t *testing.T) { assertPreviewReadFailure(t, stage) })
	}
}

// assertPreviewReadFailure 断言安全分类和无可用计划，未实现的受理端口不能被调用。
func assertPreviewReadFailure(t *testing.T, stage string) {
	t.Helper()
	p := planFixture(t)
	graph, build := planInputs(t, p)
	reader := previewDeclarations{graph: graph, build: build, stage: stage, err: project.Failure("SOURCE_UNAVAILABLE", "当前读取未确认")}
	adapter := previewAdapter{plan: p}
	code := "SOURCE_UNAVAILABLE"
	if stage == "preflight" {
		adapter.err = &mysqlruntime.Fault{Code: "DEPLOY_DATABASE_NOT_READY", Message: "原MySQL未核实"}
		code = "DEPLOY_DATABASE_NOT_READY"
	}
	if stage == "digest" {
		code = "DEPLOY_PLAN_CHANGED"
	}
	repository := receiptRepository{err: project.Failure("MUTATION_NOT_FOUND", "原键尚不存在")}
	service := NewService(repository, reader, adapter)
	command := Command{MutationID: p.Candidate.OperationID, Preview: p.Request, PlanDigest: "sha256:" + strings.Repeat("0", 64)}
	_, err := service.Accept(context.Background(), p.ProjectID, p.ServiceID, command)
	var fault *project.Error
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatal("失败读取或摘要冲突仍进入受理", code, err)
	}
}

// TestDeploymentStorageReadFailurePreserved 原键不可读时不猜测未受理；禁用后历史查询也保留失败。
func TestDeploymentStorageReadFailurePreserved(t *testing.T) {
	p := planFixture(t)
	failure := errors.New("受控存储不可用")
	service := NewService(receiptRepository{err: failure}, nil, nil)
	command := Command{MutationID: p.Candidate.OperationID, Preview: p.Request, PlanDigest: p.Digest}
	_, err := service.Accept(context.Background(), p.ProjectID, p.ServiceID, command)
	if !errors.Is(err, failure) {
		t.Fatal("原键读取失败被误当作可新受理", err)
	}
	_, err = service.Snapshot(context.Background(), p.ProjectID, p.ServiceID)
	if !errors.Is(err, failure) {
		t.Fatal("历史读取失败被隐藏", err)
	}
	_, err = service.Operation(context.Background(), p.ProjectID, p.ServiceID, p.Candidate.OperationID)
	if !errors.Is(err, failure) {
		t.Fatal("原操作读取失败被隐藏", err)
	}
}
