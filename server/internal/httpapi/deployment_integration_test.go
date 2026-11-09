//go:build integration

// 本文件通过真实构建、SQL、Compose及独立业务入口证明首次部署和原计划恢复。
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicecompose"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// url 定位本次独立服务，不使用名称或仓库路径作为运行身份。
func (f *deploymentFixture) url() string {
	return "/api/v4/projects/" + f.project.ID + "/services/" + f.project.Nodes[0].ID
}

// snapshot 穿过真实发布契约，不只检查Go可解码。
func (f *deploymentFixture) snapshot(t *testing.T) servicedeploy.Snapshot {
	t.Helper()
	return decode[servicedeploy.Snapshot](t, call(t, f.runtime.base.handler, "GET", f.url()+"/deployment", nil), 200, "DeploySnapshot")
}

// accept 先取得真实只读预览，再按该摘要受理，恢复必须关联原操作。
func (f *deploymentFixture) accept(t *testing.T, retryOf string) (servicedeploy.Command, servicedeploy.Receipt) {
	t.Helper()
	f.refreshDatabase(t)
	preview := decode[servicedeploy.Plan](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployment/preview", f.request), 200, "DeployPlan")
	command := servicedeploy.Command{MutationID: id(t), Preview: f.request, PlanDigest: preview.Digest, RetryOf: retryOf}
	value := decode[servicedeploy.Receipt](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", command), 202, "DeployReceipt")
	return command, value
}

// execute 同库领取、阶段检查点与结清均使用原目标，外部动作不嵌入SQL事务。
func (f *deploymentFixture) execute(t *testing.T, receipt servicedeploy.Receipt) (servicedeploy.Completion, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	lease, err := f.runtime.base.database.ClaimDeployment(ctx, now, now.Add(7*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != receipt.OperationID {
		t.Fatal("未领取本次真实部署", err)
	}
	fact, executeErr := f.adapter.Execute(ctx, *lease, func(ctx context.Context, point servicedeploy.Step) error {
		t.Log("真实部署", point.Name, point.State)
		valid, err := f.runtime.base.database.CheckpointDeployment(ctx, *lease, point)
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf("测试原部署执行已失效")
		}
		return nil
	})
	result := servicedeploy.Completion{State: "succeeded", Code: "READY", Message: "真实部署与业务就绪已核实", Observation: fact}
	if executeErr != nil {
		logDeploymentIsolation(t, lease.Target.Plan)
		code, message, unknown := servicedeploy.Diagnostic(executeErr)
		result.State, result.Code, result.Message = "failed", code, message
		if unknown {
			result.State = "needs_attention"
		}
	}
	if err := f.runtime.base.database.FinishDeployment(ctx, *lease, result); err != nil {
		t.Fatal("真实部署结果未结清", err)
	}
	return result, executeErr
}

// logDeploymentIsolation 仅记录本次测试容器的非敏感隔离字段和固定PATH判定，不转储原环境或挂载来源。
func logDeploymentIsolation(t *testing.T, plan servicedeploy.Plan) {
	t.Helper()
	for _, name := range []string{plan.Resources.Migration, plan.Resources.Application} {
		logOneDeploymentIsolation(t, plan, name)
	}
}

// logOneDeploymentIsolation 只输出本测试容器的端口与网络形状，不转储环境值或挂载来源。
func logOneDeploymentIsolation(t *testing.T, plan servicedeploy.Plan, name string) {
	t.Helper()
	data, err := deploymentDocker(context.Background(), plan, "container", "inspect", name)
	if err != nil {
		return
	}
	var values []struct {
		Config     struct{ Env []string }
		HostConfig struct {
			Memory, MemorySwap, NanoCpus, PidsLimit int64
			ReadonlyRootfs, Privileged              bool
			IpcMode, NetworkMode                    string
			Tmpfs                                   map[string]string
			CapDrop, SecurityOpt                    []string
			LogConfig                               struct{ Type string }
			PortBindings                            map[string][]map[string]string
		}
		NetworkSettings struct {
			Ports    map[string][]map[string]string
			Networks map[string]json.RawMessage
		}
		Mounts []struct {
			Type, Destination string
			RW                bool
		}
	}
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return
	}
	value := values[0]
	path := false
	for _, entry := range value.Config.Env {
		if entry == "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
			path = true
		}
	}
	networks := []string{}
	for key := range value.NetworkSettings.Networks {
		networks = append(networks, key)
	}
	t.Log("测试容器隔离见证", name, "环境条数", len(value.Config.Env), "固定默认PATH", path, "执行限制", value.HostConfig, "实际端口", value.NetworkSettings.Ports, "网络", networks, "挂载目标", value.Mounts)
}

// TestRealServiceDeploymentCRUDAndRecovery 用端口冲突、原键、真实CRUD、控制面重开与原容器恢复证明闭环。
func TestRealServiceDeploymentCRUDAndRecovery(t *testing.T) {
	f := openDeploymentFixture(t)
	if value := f.snapshot(t); value.Generation != "0" || value.Target != nil {
		t.Fatal("未受理服务出现虚构部署")
	}
	command, accepted := f.accept(t, "")
	t.Cleanup(func() { cleanupDeploymentContainers(t, accepted.Target.Plan) })
	replayed := decode[servicedeploy.Receipt](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", command), 202, "DeployReceipt")
	if !replayed.Replayed || !reflect.DeepEqual(replayed.Target, accepted.Target) || replayed.OperationID != accepted.OperationID {
		t.Fatal("相同部署键产生了第二个目标")
	}
	changed := command
	changed.Preview.HostPort++
	decode[any](t, call(t, f.runtime.base.handler, "POST", f.url()+"/deployments", changed), 409, "ErrorResponse")
	assertDeploymentAcceptGuards(t, f, command, accepted)
	assertDeploymentLeaseRestart(t, f, accepted)
	listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(f.request.HostPort))
	if err != nil {
		t.Fatal(err)
	}
	first, executeErr := f.execute(t, accepted)
	closeErr := listener.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if executeErr == nil || first.Code != "DEPLOY_PORT_IN_USE" || first.State != "failed" {
		t.Fatal("端口占用未明确阻断部署", executeErr, first.Code)
	}
	assertDeploymentRecoveryGuards(t, f, accepted)
	f.request.BaseGeneration = "1"
	_, resumed := f.accept(t, accepted.OperationID)
	result, err := f.execute(t, resumed)
	if err != nil || result.Observation == nil || !result.Observation.Ready {
		t.Fatal("原计划恢复未完成真实业务就绪", err)
	}
	entry := result.Observation.EntryURL
	assertTodoCRUD(t, entry)
	assertApplicationPrivileges(t, entry)
	assertMonotonicDeploymentFacts(t, f, resumed, result.Observation)
	resumed = assertNewDeploymentPending(t, f, resumed)
	before := f.snapshot(t)
	f.reopen(t)
	if after := f.snapshot(t); after.Target.ID != before.Target.ID || after.Operation.ID != before.Operation.ID || after.Observation.ContainerID != before.Observation.ContainerID {
		t.Fatal("控制面重开丢失原部署身份")
	}
	assertOriginalContainerRecovery(t, f, resumed, result.Observation)
	if sourceGit(t, f.repository, "status", "--porcelain=v1") != "" {
		t.Fatal("构建或部署改写了原业务仓库")
	}
}

// assertNewDeploymentPending 新受理不能把上次ready重新盖成近期事实；失败仍保留上次内容但标记未知。
func assertNewDeploymentPending(t *testing.T, f *deploymentFixture, previous servicedeploy.Receipt) servicedeploy.Receipt {
	t.Helper()
	f.request.BaseGeneration = previous.Target.Generation
	_, next := f.accept(t, previous.OperationID)
	value := f.snapshot(t)
	if value.Freshness != "unknown" || value.ObservationCode != "DEPLOY_PENDING" || value.Observation == nil {
		t.Fatal("新受理错误地沿用了上次ready作为当前新鲜事实")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	lease, err := f.runtime.base.database.ClaimDeployment(ctx, now, now.Add(7*time.Minute))
	if err != nil || lease == nil || lease.Operation.ID != next.OperationID {
		t.Fatal("未领取本次恢复围栏", err)
	}
	if err := f.runtime.base.database.FinishDeployment(ctx, *lease, servicedeploy.Completion{State: "needs_attention", Code: "DEPLOY_RESULT_UNKNOWN", Message: "受控未知外部结果，保留原事实"}); err != nil {
		t.Fatal(err)
	}
	value = f.snapshot(t)
	if value.Freshness != "unknown" || value.ObservationCode != "DEPLOY_RESULT_UNKNOWN" || value.Observation == nil {
		t.Fatal("失败重新刷新了旧ready事实")
	}
	f.request.BaseGeneration = next.Target.Generation
	_, restored := f.accept(t, next.OperationID)
	if _, err := f.execute(t, restored); err != nil {
		t.Fatal("原计划明确核实恢复失败", err)
	}
	return restored
}

// reopen 关闭并重新打开元数据库与原私有根，不借旧适配器指针伪造重启成功。
func (f *deploymentFixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.runtime.base.database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.build.Close(); err != nil {
		t.Fatal(err)
	}
	f.runtime.base.reopen(t)
	runtime, err := mysqlruntime.OpenDriver(os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY"), []string{f.runtime.base.root})
	if err != nil {
		t.Fatal(err)
	}
	build, err := servicebuild.OpenDriver(os.Getenv("TWINE_TEST_BUILD_DIRECTORY"), []string{f.runtime.base.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := build.Close(); err != nil {
			t.Error(err)
		}
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	f.runtime.driver, f.build = runtime, build
	f.adapter, err = servicecompose.NewDriver(f.runtime.base.database, runtime, build)
	if err != nil {
		t.Fatal(err)
	}
	f.attach(t)
}

// assertOriginalContainerRecovery 停止并明确恢复同一个应用容器，迁移任务和业务记录不被替换。
func assertOriginalContainerRecovery(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt, fact *servicedeploy.Observation) {
	t.Helper()
	plan := receipt.Target.Plan
	if _, err := deploymentDocker(context.Background(), plan, "container", "stop", "--time", "20", fact.ContainerID); err != nil {
		t.Fatal("原应用未确认停止", err)
	}
	stopped, err := f.adapter.Inspect(context.Background(), receipt.Target)
	if err != nil || stopped == nil || stopped.Running {
		t.Fatal("被动采集未显示真实停止", err)
	}
	if err := f.runtime.base.database.ObserveDeployment(context.Background(), captureDeploymentTarget(t, f.runtime.base.database, receipt.Target), stopped, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.request.BaseGeneration = receipt.Target.Generation
	_, next := f.accept(t, receipt.OperationID)
	result, err := f.execute(t, next)
	if err != nil || result.Observation == nil || result.Observation.ContainerID != fact.ContainerID || !result.Observation.Ready {
		t.Fatal("恢复没有沿用原业务容器", err)
	}
	var todos []map[string]any
	businessRequest(t, result.Observation.EntryURL, "GET", "/api/todos", nil, 200, &todos)
	if len(todos) != 1 || todos[0]["title"] != "重启后保留" {
		t.Fatal("服务或控制面恢复丢失业务数据")
	}
	assertDeploymentDatabaseDisconnect(t, f, next)
}

// deploymentDocker 只操作测试计划的固定context；不回显Docker原输出。
func deploymentDocker(ctx context.Context, plan servicedeploy.Plan, args ...string) ([]byte, error) {
	return localdocker.Run(ctx, localdocker.Policy{Timeout: 45 * time.Second}, append([]string{"--context", plan.Environment.Context}, args...)...)
}

// cleanupDeploymentContainers 删除前核实随机安装、完整角色标签和真实ID；不删除数据库、卷或其他服务。
func cleanupDeploymentContainers(t *testing.T, plan servicedeploy.Plan) {
	t.Helper()
	for role, name := range map[string]string{"application": plan.Resources.Application, "migration": plan.Resources.Migration} {
		cleanupOneDeploymentContainer(t, plan, role, name)
	}
}

// cleanupOneDeploymentContainer 核实单一测试容器，任何不确定结果仅保留该对象并报告。
func cleanupOneDeploymentContainer(t *testing.T, plan servicedeploy.Plan, role, name string) {
	t.Helper()
	data, err := deploymentDocker(context.Background(), plan, "container", "ls", "--all", "--filter", "name="+name, "--format", "{{.Names}}")
	if err != nil {
		t.Error("测试业务容器名单未确认", err)
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	if string(bytes.TrimSpace(data)) != name {
		t.Error("测试业务容器名称不唯一，保留现场")
		return
	}
	data, err = deploymentDocker(context.Background(), plan, "container", "inspect", name)
	if err != nil {
		t.Error("测试业务容器归属未确认", err)
		return
	}
	var values []struct {
		ID, Name string
		Config   struct{ Labels map[string]string }
	}
	if json.Unmarshal(data, &values) != nil || len(values) != 1 || values[0].Name != "/"+name {
		t.Error("测试业务容器返回不完整，保留现场")
		return
	}
	labels := values[0].Config.Labels
	if labels["io.twine-realm.deploy.installation"] != plan.Environment.RuntimeInstallationID || labels["io.twine-realm.deploy.project"] != plan.ProjectID || labels["io.twine-realm.deploy.service"] != plan.ServiceID || labels["io.twine-realm.deploy.resource"] != plan.Database.ResourceID || labels["io.twine-realm.deploy.runtime"] != plan.RuntimeDigest || labels["io.twine-realm.deploy.role"] != role || len(values[0].ID) != 64 {
		t.Error("测试业务容器完整归属不匹配，保留原对象")
		return
	}
	if _, err := deploymentDocker(context.Background(), plan, "container", "rm", "--force", values[0].ID); err != nil {
		t.Error("本次业务容器未清理", err)
	}
}

// businessRequest 使用独立入口，不发送控制面的Origin或请求头，不将错误响应原文写入日志。
func businessRequest(t *testing.T, entry, method, path string, body any, status int, result any) {
	t.Helper()
	var input []byte
	var err error
	if body != nil {
		input, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, entry+path, bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("独立业务入口不可访问", err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(data) > 8192 || response.StatusCode != status {
		t.Fatalf("业务请求%s %s期望%d，实际%d", method, path, status, response.StatusCode)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal("业务响应无效")
		}
	}
}

// assertTodoCRUD 真实业务入口验证创建幂等、读取、修改、删除和重启保留见证。
func assertTodoCRUD(t *testing.T, entry string) {
	t.Helper()
	first, second := id(t), id(t)
	input := map[string]any{"id": first, "title": "首次Todo"}
	businessRequest(t, entry, "POST", "/api/todos", input, 201, nil)
	businessRequest(t, entry, "POST", "/api/todos", input, 200, nil)
	var values []map[string]any
	businessRequest(t, entry, "GET", "/api/todos", nil, 200, &values)
	if len(values) != 1 || values[0]["id"] != first {
		t.Fatal("同ID重复创建产生第二条业务记录")
	}
	businessRequest(t, entry, "PUT", "/api/todos/"+first, map[string]any{"title": "重启后保留", "completed": true}, 204, nil)
	businessRequest(t, entry, "POST", "/api/todos", map[string]any{"id": second, "title": "待删除"}, 201, nil)
	businessRequest(t, entry, "DELETE", "/api/todos/"+second, nil, 204, nil)
	businessRequest(t, entry, "GET", "/api/todos", nil, 200, &values)
	if len(values) != 1 || values[0]["title"] != "重启后保留" || values[0]["completed"] != true {
		t.Fatal("真实CRUD结果不一致")
	}
}

// assertApplicationPrivileges 从真实业务进程验证权限拒绝与角色秘密隔离。
func assertApplicationPrivileges(t *testing.T, entry string) {
	t.Helper()
	var result struct {
		Denied   int  `json:"denied"`
		Isolated bool `json:"isolated"`
	}
	businessRequest(t, entry, "GET", "/test/permissions", nil, 200, &result)
	if result.Denied != 4 || !result.Isolated {
		t.Fatal("真实应用身份获得了额外权限或其他用途秘密")
	}
}
