//go:build integration

// 本文件验证实际存储中的原键竞争、受理冲突、控制面退出和旧执行围栏。
package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentInput 与公开受理用例相同地计算规范意图，不修改持久回执或使用任意哈希占位。
func deploymentInput(t *testing.T, plan servicedeploy.Plan, command servicedeploy.Command) servicedeploy.AcceptedInput {
	t.Helper()
	data, err := json.Marshal(struct {
		Project, Service string
		Command          servicedeploy.Command
	}{plan.ProjectID, plan.ServiceID, command})
	if err != nil {
		t.Fatal(err)
	}
	return servicedeploy.AcceptedInput{Plan: plan, Command: command, Hash: sha256.Sum256(data)}
}

// assertDeploymentAcceptGuards 仓储自身也守住原键和代次，HTTP预检不作为唯一一致性屏障。
func assertDeploymentAcceptGuards(t *testing.T, f *deploymentFixture, command servicedeploy.Command, receipt servicedeploy.Receipt) {
	t.Helper()
	store := f.runtime.base.database
	input := deploymentInput(t, receipt.Target.Plan, command)
	replayed, err := store.AcceptDeployment(context.Background(), input)
	if err != nil || !replayed.Replayed || replayed.OperationID != receipt.OperationID || replayed.Target.ID != receipt.Target.ID {
		t.Fatal("仓储原键竞争没有重放同一持久结果", err)
	}
	changed := command
	changed.Preview.HostPort = 8081
	if changed.Preview.HostPort == command.Preview.HostPort {
		changed.Preview.HostPort = 8082
	}
	_, err = store.AcceptDeployment(context.Background(), deploymentInput(t, receipt.Target.Plan, changed))
	assertDeploymentConflict(t, err, "IDEMPOTENCY_CONFLICT")
	changed = command
	changed.MutationID = id(t)
	_, err = store.AcceptDeployment(context.Background(), deploymentInput(t, receipt.Target.Plan, changed))
	assertDeploymentConflict(t, err, "DEPLOY_GENERATION_CONFLICT")
	buildFailure(t, call(t, f.runtime.base.handler, "GET", "/api/v4/deployment-mutations/"+changed.MutationID, nil), 404, "MUTATION_NOT_FOUND")
	request := f.request
	request.BaseGeneration = receipt.Target.Generation
	preview := decode[servicedeploy.Plan](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployment/preview", request), 200, "DeployPlan")
	busy := servicedeploy.Command{MutationID: id(t), Preview: request, PlanDigest: preview.Digest, RetryOf: receipt.OperationID}
	buildFailure(t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", busy), 409, "DEPLOY_BUSY")
}

// assertDeploymentConflict 断言稳定分类而非驱动文案，失败必须保持明确且不可当成功。
func assertDeploymentConflict(t *testing.T, err error, code string) {
	t.Helper()
	var fault *project.Error
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatal("部署冲突分类不一致", code, err)
	}
}

// assertDeploymentLeaseRestart 真正关闭并重开存储和驱动，原目标重新领取，旧执行仍不能落库。
func assertDeploymentLeaseRestart(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	old, err := f.runtime.base.database.ClaimDeployment(ctx, now, now.Add(7*time.Minute))
	if err != nil || old == nil || old.Operation.ID != receipt.OperationID {
		t.Fatal("原部署未领取", err)
	}
	if err := f.runtime.base.database.YieldDeployment(ctx, *old); err != nil {
		t.Fatal("退出未保留原部署", err)
	}
	f.reopen(t)
	now = time.Now().UTC()
	current, err := f.runtime.base.database.ClaimDeployment(ctx, now, now.Add(7*time.Minute))
	if err != nil || current == nil || !current.Recover || current.Target.ID != old.Target.ID || current.Operation.ID != old.Operation.ID || current.Execution <= old.Execution {
		t.Fatal("重启未领取同一原目标的新执行", err)
	}
	valid, err := f.runtime.base.database.CheckpointDeployment(ctx, *old, servicedeploy.Step{Name: "preflight", State: "executing", UpdatedAt: mysqlruntime.Timestamp(time.Now())})
	if err != nil || valid {
		t.Fatal("旧执行仍可提交检查点", err)
	}
	if err := f.runtime.base.database.FinishDeployment(ctx, *old, servicedeploy.Completion{State: "needs_attention", Code: "OLD_EXECUTION", Message: "旧执行不得结清当前操作"}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Operation == nil || snapshot.Operation.State != "executing" || snapshot.Operation.Version != current.Operation.Version {
		t.Fatal("旧执行覆盖了重启后的操作")
	}
	if err := f.runtime.base.database.YieldDeployment(ctx, *current); err != nil {
		t.Fatal("第二次退出没有保留原身份", err)
	}
}

// assertDeploymentRecoveryGuards 恢复必须指向原操作与物理配置，不能借重试更换入口。
func assertDeploymentRecoveryGuards(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt) {
	t.Helper()
	f.refreshDatabase(t)
	request := f.request
	request.BaseGeneration = receipt.Target.Generation
	preview := decode[servicedeploy.Plan](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployment/preview", request), 200, "DeployPlan")
	command := servicedeploy.Command{MutationID: id(t), Preview: request, PlanDigest: preview.Digest}
	buildFailure(t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", command), 409, "DEPLOY_RETRY_CONFLICT")
	request.HostPort = 8081
	if request.HostPort == f.request.HostPort {
		request.HostPort = 8082
	}
	preview = decode[servicedeploy.Plan](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployment/preview", request), 200, "DeployPlan")
	command = servicedeploy.Command{MutationID: id(t), Preview: request, PlanDigest: preview.Digest, RetryOf: receipt.OperationID}
	buildFailure(t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", command), 409, "DEPLOY_REPLACEMENT_UNSUPPORTED")
}
