//go:build integration

// 本文件通过真实迁移失败、dirty与数据库断连验证阻塞和数据保留，不清dirty或重置账号。
package httpapi_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentContainerID 只查询本次固定名称，身份由生产适配器与清理器另行完整核实。
func deploymentContainerID(t *testing.T, plan servicedeploy.Plan, name string) string {
	t.Helper()
	data, err := deploymentDocker(context.Background(), plan, "container", "inspect", "--format={{json .Id}}", name)
	if err != nil {
		t.Fatal("本次容器身份查询失败", err)
	}
	var value string
	if json.Unmarshal(data, &value) != nil || len(value) != 64 {
		t.Fatal("本次容器身份不完整")
	}
	return value
}

// deploymentSQL 只在本次受信测试库查询可公开断言的见证，不传密码、不输出原始响应。
func deploymentSQL(t *testing.T, plan servicedeploy.Plan, query, expected string) {
	t.Helper()
	data, err := deploymentDocker(context.Background(), plan, "exec", plan.Database.Container, "mysql", "--defaults-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute", query)
	if err != nil || strings.TrimSpace(string(data)) != expected {
		t.Fatal("本次数据库见证未成立", err)
	}
}

// TestRealServiceMigrationFailureRetainsDirty 两次明确核实仍保留同一失败任务和部分DDL，不盲重跑。
func TestRealServiceMigrationFailureRetainsDirty(t *testing.T) {
	f := openDeploymentScenario(t, true)
	_, first := f.accept(t, "")
	plan := first.Target.Plan
	t.Cleanup(func() { cleanupDeploymentContainers(t, plan) })
	result, err := f.execute(t, first)
	if err == nil || result.Code != "DEPLOY_MIGRATION_DIRTY" || result.State != "needs_attention" {
		t.Fatal("真实失败迁移未保留dirty", result.Code, err)
	}
	original := deploymentContainerID(t, plan, plan.Resources.Migration)
	assertDirtyDeploymentHistory(t, plan)
	f.request.BaseGeneration = first.Target.Generation
	_, retried := f.accept(t, first.OperationID)
	result, err = f.execute(t, retried)
	if err == nil || result.Code != "DEPLOY_MIGRATION_DIRTY" || result.State != "needs_attention" {
		t.Fatal("核实原失败任务错误地产生成功", result.Code, err)
	}
	if deploymentContainerID(t, plan, plan.Resources.Migration) != original {
		t.Fatal("dirty恢复更换或重跑了迁移任务")
	}
	assertDirtyDeploymentHistory(t, plan)
	value := f.snapshot(t)
	if value.Observation != nil && value.Observation.Ready {
		t.Fatal("dirty服务被显示为就绪")
	}
}

// assertDirtyDeploymentHistory 部分DDL仍保留；模板只在全部Up成功后记录摘要，失败不能补造完成证据。
func assertDirtyDeploymentHistory(t *testing.T, plan servicedeploy.Plan) {
	t.Helper()
	db := "`" + plan.Binding.DatabaseName + "`"
	for _, witness := range []struct{ name, query, expected string }{
		{"保留dirty版本", "SELECT CONCAT(version,':',dirty) FROM " + db + ".schema_migrations", "2:1"},
		{"保留部分业务表", "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='" + plan.Binding.DatabaseName + "' AND TABLE_NAME='todos'", "1"},
		{"不补造摘要", "SELECT COUNT(*) FROM " + db + ".service_migration_digests", "0"},
	} {
		t.Run(witness.name, func(t *testing.T) { deploymentSQL(t, plan, witness.query, witness.expected) })
	}
}

// assertDeploymentDatabaseDisconnect 数据库真正停止时入口和采集都不能报告就绪，原卷恢复后数据仍在。
func assertDeploymentDatabaseDisconnect(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt) {
	t.Helper()
	plan := receipt.Target.Plan
	if _, err := deploymentDocker(context.Background(), plan, "container", "stop", "--time", "20", plan.Database.Container); err != nil {
		t.Fatal("本次数据库未停止", err)
	}
	if fact, err := f.adapter.Inspect(context.Background(), receipt.Target); err == nil || fact != nil && fact.Ready {
		t.Fatal("数据库断连仍显示就绪", err)
	}
	businessRequest(t, "http://127.0.0.1:"+strconv.Itoa(plan.Request.HostPort), "GET", "/readyz", nil, 503, nil)
	if _, err := deploymentDocker(context.Background(), plan, "container", "start", plan.Database.Container); err != nil {
		t.Fatal("本次原数据库未启动", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		fact, err := f.adapter.Inspect(ctx, receipt.Target)
		if err == nil && fact != nil && fact.Ready {
			assertDeploymentRecoveredData(t, plan, fact)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("原数据库恢复未就绪", err)
		case <-ticker.C:
		}
	}
}

// assertDeploymentRecoveredData 数据身份与业务记录分别核实，不能用新空库伪造恢复。
func assertDeploymentRecoveredData(t *testing.T, plan servicedeploy.Plan, fact *servicedeploy.Observation) {
	t.Helper()
	if fact.DatabaseUUID != plan.Database.ServerUUID {
		t.Fatal("数据库恢复更换了原数据身份")
	}
	var todos []map[string]any
	businessRequest(t, fact.EntryURL, "GET", "/api/todos", nil, 200, &todos)
	if len(todos) != 1 || todos[0]["title"] != "重启后保留" {
		t.Fatal("数据库恢复丢失业务记录")
	}
}
