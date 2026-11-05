//go:build integration

// 本文件验证真实并发受理、固定配置边界和损坏持久计划的拒绝，不产生Docker副作用。
package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// admissionInput 直接测试持久化端口的竞争，使用已规范化真实目录配置与受控意图摘要。
func admissionInput(t *testing.T, f *runtimeFixture) mysqlruntime.AcceptedInput {
	t.Helper()
	hash, err := mysqlruntime.Fingerprint(struct {
		Definition    project.DefinitionRef
		Configuration map[string]any
	}{f.component.Definition, f.component.Configuration})
	if err != nil {
		t.Fatal(err)
	}
	resource := mysqlruntime.Resource{ProjectID: f.current.ID, ComponentID: f.component.ID, InstallationID: f.driver.InstallationID(), DaemonID: f.driver.Environment().DaemonID, Definition: f.component.Definition, Configuration: f.component.Configuration, ConfigurationHash: hash, Image: mysqlruntime.Image}
	return mysqlruntime.AcceptedInput{ProjectID: f.current.ID, ComponentID: f.component.ID, Command: f.command, Hash: [32]byte{1}, Resource: resource}
}

// TestRuntimeConcurrentAdmissionIsExactlyOneTarget 真实唯一键竞争只产生一套目标和事件，重放以原事务结果为准。
func TestRuntimeConcurrentAdmissionIsExactlyOneTarget(t *testing.T) {
	f := openRuntimeFixture(t)
	input := admissionInput(t, f)
	type result struct {
		receipt mysqlruntime.Receipt
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			receipt, err := f.base.database.AcceptRuntime(ctx, input)
			results <- result{receipt, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	originals := 0
	identity := ""
	for value := range results {
		if value.err != nil {
			t.Fatal(value.err)
		}
		if !value.receipt.Replayed {
			originals++
		}
		if identity != "" && identity != value.receipt.Target.ID {
			t.Fatal("并发原键创建了两个目标")
		}
		identity = value.receipt.Target.ID
	}
	if value := f.snapshot(t); originals != 1 || value.Generation != "1" || value.Sequence != "1" {
		t.Fatal("同键竞争部分推进了代次或事件")
	}
	input.Hash = [32]byte{2}
	_, err := f.base.database.AcceptRuntime(context.Background(), input)
	var conflict *project.Error
	if !errors.As(err, &conflict) || conflict.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("相同键的另一意图未冲突")
	}
	input.Command.MutationID = id(t)
	_, err = f.base.database.AcceptRuntime(context.Background(), input)
	if !errors.As(err, &conflict) || conflict.Code != "RUNTIME_GENERATION_CONFLICT" {
		t.Fatal("新请求覆盖了已推进的运行代次")
	}
	decode[any](t, call(t, f.base.handler, "GET", "/api/v1/runtime-mutations/"+input.Command.MutationID, nil), 404, "ErrorResponse")
}

// TestRuntimeConfigurationLockDoesNotBlockStop 后来声明不能偷换原数据库配置，停止仍使用原资源且记录新画布出处。
func TestRuntimeConfigurationLockDoesNotBlockStop(t *testing.T) {
	f := openRuntimeFixture(t)
	accepted := f.accept(t, f.command)
	change := middlewareCommand(t, f.base, f.current)
	change.Configuration = map[string]any{"memoryMiB": 1024, "runtimeProfile": "local-compose-v1"}
	updated := decode[project.Receipt](t, call(t, f.base.handler, "PUT", canvasURL(f.current)+"/middlewares/"+f.component.ID, change), 200, "CanvasReceipt")
	command := f.command
	command.MutationID = id(t)
	command.BaseRevision = updated.Project.Revision
	command.BaseGeneration = "1"
	command.RetryOf = accepted.OperationID
	failure := decode[struct{ Error project.Error }](t, call(t, f.base.handler, "POST", f.targetURL(), command), 409, "ErrorResponse")
	if failure.Error.Code != "RUNTIME_CONFIGURATION_LOCKED" {
		t.Fatal("配置变化未由固定配置边界拒绝")
	}
	if value := f.snapshot(t); value.Generation != "1" || value.Target.Resource.ConfigurationHash != accepted.Target.Resource.ConfigurationHash {
		t.Fatal("声明修改偷偷替换了固定运行配置")
	}
	command.MutationID = id(t)
	command.Desired = "stopped"
	stopped := f.accept(t, command)
	if stopped.Target.Resource.ID != accepted.Target.Resource.ID || stopped.Target.Resource.ConfigurationHash != accepted.Target.Resource.ConfigurationHash || stopped.Target.CanvasRevision != updated.Project.Revision {
		t.Fatal("停止使用了后来配置或丢失声明出处")
	}
	if err := f.base.database.BindRuntime(context.Background(), id(t), f.driver.Environment().DaemonID); err == nil {
		t.Fatal("另一安装身份接管了原数据")
	}
	if err := f.base.database.BindRuntime(context.Background(), f.driver.InstallationID(), "another-daemon"); err == nil {
		t.Fatal("另一daemon接管了原数据")
	}
	if err := f.base.database.BindRuntime(context.Background(), f.driver.InstallationID(), f.driver.Environment().DaemonID); err != nil {
		t.Fatal("失败接管破坏了原安装记录")
	}
}

// TestCorruptRuntimeTargetCannotExecute 计划不完整、含未知字段或索引归属不符时不领取，也不部分推进操作。
func TestCorruptRuntimeTargetCannotExecute(t *testing.T) {
	f := openRuntimeFixture(t)
	accepted := f.accept(t, f.command)
	var original []byte
	if err := f.sql.QueryRow("SELECT payload FROM runtime_targets WHERE id=?", accepted.Target.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.sql.Exec("UPDATE runtime_targets SET payload=? WHERE id=?", original, accepted.Target.ID); err != nil {
			t.Error(err)
		}
	})
	for _, payload := range corruptTargetPayloads(t, original, accepted.Target) {
		if _, err := f.sql.Exec("UPDATE runtime_targets SET payload=? WHERE id=?", payload, accepted.Target.ID); err != nil {
			t.Fatal(err)
		}
		decode[any](t, call(t, f.base.handler, "GET", "/api/v1/projects/"+f.current.ID+"/runtime", nil), 503, "ErrorResponse")
		if lease, err := f.base.database.ClaimRuntime(context.Background(), time.Now(), time.Now().Add(time.Minute)); err == nil || lease != nil {
			t.Fatal("损坏计划仍被领取执行")
		}
	}
	if _, err := f.sql.Exec("UPDATE runtime_targets SET payload=? WHERE id=?", original, accepted.Target.ID); err != nil {
		t.Fatal(err)
	}
	if value := f.snapshot(t); value.Operation.State != "accepted" || value.Operation.Version != "1" || value.Sequence != "1" {
		t.Fatal("拒绝损坏计划时仍部分推进了执行")
	}
}

// corruptTargetPayloads 明确生成结构、未知字段、归属和载荷大小四类不可信持久输入。
func corruptTargetPayloads(t *testing.T, original []byte, target mysqlruntime.Target) [][]byte {
	t.Helper()
	var unknown map[string]any
	if err := json.Unmarshal(original, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["unexpected"] = true
	extra, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	target.ProjectID = id(t)
	misowned, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	oversized, err := json.Marshal(strings.Repeat("x", 65537))
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{[]byte(`"incomplete"`), extra, misowned, oversized}
}
