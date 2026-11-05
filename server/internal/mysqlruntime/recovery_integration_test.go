//go:build integration

// 本文件穿过真实Compose制造响应丢失；同库事务和执行围栏另由真实SQL检查负责。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// realRuntimeDriver 只使用平台门禁冻结的新安装根，最终资源由该门禁核实归属后清理。
func realRuntimeDriver(t *testing.T) *Driver {
	t.Helper()
	path := os.Getenv("TWINE_TEST_RUNTIME_DIRECTORY")
	if path == "" {
		t.Fatal("真实运行检查必须由平台门禁注入独立私有根")
	}
	driver, err := OpenDriver(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

// realLease 每次测试使用全新归属，绝不指向用户项目或已有数据库。
func realLease(t *testing.T, driver *Driver) Lease {
	t.Helper()
	resource := configuredResource(t, driver)
	id, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return Lease{Target: Target{ID: id, ProjectID: resource.ProjectID, CanvasRevision: "1", Generation: "1", Desired: "running", Resource: resource, CreatedAt: Timestamp(time.Now())}, Operation: Operation{ID: id, ProjectID: resource.ProjectID, Generation: "1", Attempts: 1}, Execution: "1"}
}

// executeRealGoal 认证就绪只来自真实执行器，本地回调仅记录测试已授予执行权限。
func executeRealGoal(t *testing.T, driver *Driver, lease Lease) *Observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	fact, err := driver.Execute(ctx, lease, 120*time.Second, func(context.Context, Checkpoint) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("真实目标执行失败：%v", err)
	}
	return fact
}

// queryWitness 使用固定客户端秘密引用，不在argv、诊断或元数据库传递密码值。
func queryWitness(t *testing.T, driver *Driver, containerID, sql string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := driver.execute(ctx, "--context", driver.Environment().Context, "exec", containerID, "mysql", "--defaults-extra-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute="+sql)
	if err != nil {
		t.Fatal("真实数据见证查询失败")
	}
	return strings.TrimSpace(string(data))
}

// TestRealPartialCreationRecovery 丢失创建响应后，重新打开驱动核实原卷和容器，密码、数据与server_uuid不漂移。
func TestRealPartialCreationRecovery(t *testing.T) {
	for _, phase := range []string{"volume", "container"} {
		t.Run(phase, func(t *testing.T) { recoverPartialCreation(t, phase) })
	}
}

// recoverPartialCreation 在未知结果后重新打开同一私有根，不改原资源身份或秘密。
func recoverPartialCreation(t *testing.T, phase string) {
	t.Helper()
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	loseCreationReply(t, driver, lease, phase)
	prefix := projectFilePrefix + lease.Target.Resource.ID + "/" + passwordFileName
	secret, err := driver.readPrivate(prefix, 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	driver = realRuntimeDriver(t)
	lease.Operation.Attempts, lease.Execution = 2, "2"
	fact := executeRealGoal(t, driver, lease)
	if fact == nil || !fact.Ready || !fact.VolumeFound || fact.ServerUUID == "" {
		t.Fatal("恢复没有真正认证就绪")
	}
	after, err := driver.readPrivate(prefix, 128)
	if err != nil || !bytes.Equal(secret, after) {
		t.Fatal("部分成功恢复重置了原密码")
	}
	assertRealStopRecovery(t, driver, lease, fact)
}

// loseCreationReply 先实际创建成功再丢失结果，不用模拟创建证明恢复。
func loseCreationReply(t *testing.T, driver *Driver, lease Lease, phase string) {
	t.Helper()
	lost := false
	driver.execute = func(ctx context.Context, args ...string) ([]byte, error) {
		data, err := localDocker(ctx, args...)
		creation := len(args) > 3 && ((phase == "volume" && args[2] == "volume" && args[3] == "create") || (phase == "container" && args[2] == "compose" && contains(args, "up")))
		if err == nil && creation && !lost {
			lost = true
			return nil, failure("RUNTIME_RESULT_UNKNOWN", "受控创建响应丢失，须核实原资源", true, true)
		}
		return data, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err := driver.Execute(ctx, lease, 120*time.Second, func(context.Context, Checkpoint) (bool, error) { return true, nil })
	cancel()
	var fault *Fault
	if !lost || !errors.As(err, &fault) || fault.Code != "RUNTIME_RESULT_UNKNOWN" {
		t.Fatal("未在真实创建成功后制造响应丢失")
	}
}

// assertRealStopRecovery 用真实写入见证停止后仍为原数据库，停止不删除卷或自动换实例。
func assertRealStopRecovery(t *testing.T, driver *Driver, lease Lease, fact *Observation) {
	t.Helper()
	queryWitness(t, driver, fact.ContainerID, "CREATE DATABASE recovery_witness; CREATE TABLE recovery_witness.marker(value VARCHAR(32)); INSERT INTO recovery_witness.marker VALUES('retained')")
	lease.ServerUUID, lease.VolumeConfirmed = fact.ServerUUID, true
	lease.Target.Generation, lease.Operation.Generation, lease.Target.Desired, lease.Operation.Attempts = "2", "2", "stopped", 1
	stopped := executeRealGoal(t, driver, lease)
	if stopped.Running || !stopped.VolumeFound {
		t.Fatal("明确停止未保留原卷")
	}
	lease.Target.Generation, lease.Operation.Generation, lease.Target.Desired = "3", "3", "running"
	resumed := executeRealGoal(t, driver, lease)
	if resumed.ServerUUID != fact.ServerUUID || resumed.ContainerID != fact.ContainerID || queryWitness(t, driver, resumed.ContainerID, "SELECT value FROM recovery_witness.marker") != "retained" {
		t.Fatal("恢复产生了第二实例或丢失原数据")
	}
}

// TestRealForeignNetworkRemainsUntouched 固定名称被其他归属占用时明确拒绝，不接管或删除对象。
func TestRealForeignNetworkRemainsUntouched(t *testing.T) {
	driver := realRuntimeDriver(t)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := realLease(t, driver)
	nonce, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := localDocker(ctx, "--context", driver.Environment().Context, "network", "create", "--internal", "--label", "io.twine-realm.foreign-test="+nonce, lease.Target.Resource.Network)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSpace(string(data))
	if !dockerIdentity.MatchString(identity) {
		t.Fatal("临时外来归属网络的ID未确认；保留现场")
	}
	t.Cleanup(func() { cleanupForeignNetwork(t, driver, identity, nonce) })
	_, err = driver.Execute(ctx, lease, time.Second, func(context.Context, Checkpoint) (bool, error) { return true, nil })
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "RUNTIME_OWNERSHIP_CONFLICT" {
		t.Fatal("实际外来归属冲突未被拒绝")
	}
	if _, err := localDocker(ctx, "--context", driver.Environment().Context, "network", "inspect", identity); err != nil {
		t.Fatal("冲突处理删除了原网络")
	}
}

// cleanupForeignNetwork 只按本测试随机标签和不可变ID清理，不借产品归属授权删除外来网络。
func cleanupForeignNetwork(t *testing.T, driver *Driver, identity, nonce string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := localDocker(ctx, "--context", driver.Environment().Context, "network", "inspect", identity, "--format", "{{index .Labels \"io.twine-realm.foreign-test\"}}")
	if err != nil || strings.TrimSpace(string(data)) != nonce {
		t.Error("测试网络清理前归属不符，保留现场")
		return
	}
	if _, err := localDocker(ctx, "--context", driver.Environment().Context, "network", "rm", identity); err != nil {
		t.Error("临时归属冲突网络未能清理，保留现场")
	}
}
