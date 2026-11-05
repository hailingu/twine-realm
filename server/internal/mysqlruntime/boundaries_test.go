// 本文件验证容器边界、跨进程锁、采样时间与恢复分类，不把协议替身当成真实Docker证据。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/catalog"
)

// TestDesktopMountMapping 只对已固定的Mac Desktop识别VM前缀，异源路径与其他daemon仍拒绝。
func TestDesktopMountMapping(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	expected := filepath.Join(driver.directory, "projects", "example", "root.secret")
	if driver.matchingBindSource("/host_mnt"+expected, expected) {
		t.Fatal("普通daemon接受了Desktop别名")
	}
	driver.installation.Environment.BindSourceMode = "desktop-host-mnt-v1"
	if driver.matchingBindSource("/host_mnt"+expected, expected) != (runtime.GOOS == "darwin") {
		t.Fatal("固定Desktop映射没有遵守宿主平台边界")
	}
	for _, actual := range []string{"/host_mnt/foreign/root.secret", "/host_mnt" + expected + ".other", "/host_mnt/host_mnt" + expected} {
		if driver.matchingBindSource(actual, expected) {
			t.Fatal("额外前缀或异源秘密被接受")
		}
	}
}

// configuredResource 从真实目录归一化得到可执行配置，不维护另一份生产配置规则。
func configuredResource(t *testing.T, driver *Driver) Resource {
	t.Helper()
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	resource := testResource(t, driver)
	resource.Definition = definitions.List().Definitions[0].Reference
	configuration, reasons, err := definitions.Normalize(resource.Definition, map[string]any{"runtimeProfile": "local-compose-v1"})
	if err != nil || len(reasons) != 0 {
		t.Fatal("真实目录未归一化运行配置")
	}
	resource.Configuration = configuration
	resource.ConfigurationHash, err = Fingerprint(struct {
		Definition    any
		Configuration map[string]any
	}{resource.Definition, configuration})
	if err != nil {
		t.Fatal(err)
	}
	return resource
}

// containerFixture 只提供静态边界输入；实际Compose输出由端到端检查独立验证。
func containerFixture(t *testing.T, driver *Driver, resource Resource) (imageSpec, containerState) {
	t.Helper()
	var image imageSpec
	image.ID = testEnvironment().ImageID
	image.Config.Env = []string{"PATH=/tools"}
	image.Config.Entrypoint = []string{"docker-entrypoint.sh"}
	value := containerState{ID: strings.Repeat("a", 64), Name: "/" + resource.Container, Image: image.ID}
	value.Config.Image, value.Config.Labels = Image, labels(resource)
	value.Config.Env = append([]string{"MYSQL_ROOT_PASSWORD_FILE=/run/secrets/root_password"}, image.Config.Env...)
	value.Config.Entrypoint = image.Config.Entrypoint
	value.Config.Cmd = []string{"mysqld", "--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci"}
	value.Config.Healthcheck.Test = []string{"CMD", "mysql", "--defaults-extra-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute=SELECT 1"}
	value.Config.Healthcheck.Interval, value.Config.Healthcheck.Timeout, value.Config.Healthcheck.StartPeriod, value.Config.Healthcheck.Retries = int64(5*time.Second), int64(3*time.Second), int64(5*time.Second), 3
	value.HostConfig.Memory, value.HostConfig.IpcMode, value.HostConfig.NetworkMode = 512*1024*1024, "private", resource.Network
	value.HostConfig.RestartPolicy.Name, value.HostConfig.SecurityOpt = "unless-stopped", []string{"no-new-privileges:true"}
	value.NetworkSettings.Networks = map[string]any{resource.Network: map[string]any{}}
	value.NetworkSettings.Ports = map[string]any{"3306/tcp": nil, "33060/tcp": nil}
	prefix := filepath.Join(driver.directory, "projects", resource.ID)
	data, err := json.Marshal([]map[string]any{
		{"Type": "volume", "Name": resource.Volume, "Destination": "/var/lib/mysql", "RW": true},
		{"Type": "bind", "Source": filepath.Join(prefix, "root.secret"), "Destination": "/run/secrets/root_password", "RW": false},
		{"Type": "bind", "Source": filepath.Join(prefix, "client.secret"), "Destination": "/run/secrets/client_options", "RW": false},
	})
	if err != nil || json.Unmarshal(data, &value.Mounts) != nil {
		t.Fatal("无法构造边界挂载")
	}
	value.State.Status, value.State.Running = "running", true
	return image, value
}

// TestContainerOwnershipAndIsolation 归属标签相同也不接受额外权限、端口、命令或秘密来源。
func TestContainerOwnershipAndIsolation(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	image, value := containerFixture(t, driver, resource)
	if err := driver.validateContainer(resource, image, value); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		change func(*containerState)
	}{
		{"镜像", func(v *containerState) { v.Image = "sha256:" + strings.Repeat("b", 64) }},
		{"归属", func(v *containerState) { delete(v.Config.Labels, "io.twine-realm.installation") }},
		{"命令", func(v *containerState) { v.Config.Cmd = []string{"sh"} }},
		{"密码环境", func(v *containerState) {
			v.Config.Env = append(v.Config.Env, "MYSQL_ROOT_PASSWORD=synthetic-not-a-secret")
		}},
		{"重复环境", func(v *containerState) { v.Config.Env = append(v.Config.Env, v.Config.Env[0]) }},
		{"特权", func(v *containerState) { v.HostConfig.Privileged = true }},
		{"主机网络", func(v *containerState) { v.HostConfig.NetworkMode = "host" }},
		{"额外权限", func(v *containerState) { v.HostConfig.CapAdd = []string{"SYS_ADMIN"} }},
		{"主机端口", func(v *containerState) { v.HostConfig.PortBindings = map[string]any{"3306/tcp": []any{}} }},
		{"实际端口", func(v *containerState) {
			v.NetworkSettings.Ports["3306/tcp"] = []any{map[string]string{"HostPort": "3306"}}
		}},
		{"额外网络", func(v *containerState) { v.NetworkSettings.Networks["foreign"] = map[string]any{} }},
		{"移除网络", func(v *containerState) { v.NetworkSettings.Networks = map[string]any{} }},
		{"额外挂载", func(v *containerState) { v.Mounts = append(v.Mounts, v.Mounts[0]) }},
		{"替换卷", func(v *containerState) { v.Mounts[0].Name = "foreign" }},
		{"可写秘密", func(v *containerState) { v.Mounts[1].RW = true }},
		{"异源秘密", func(v *containerState) { v.Mounts[1].Source = "/foreign/root.secret" }},
		{"错误探针", func(v *containerState) { v.Config.Healthcheck.Test = []string{"CMD", "true"} }},
		{"未知状态", func(v *containerState) { v.State.Status = "dead" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var changed containerState
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			scenario.change(&changed)
			if err := driver.validateContainer(resource, image, changed); err == nil {
				t.Fatal("越界容器被接受")
			}
		})
	}
}

// TestProjectLockAcrossDrivers 模拟两个控制面进程打开同一根；前一个释放前第二个不能获得写权限。
func TestProjectLockAcrossDrivers(t *testing.T) {
	first, path := openPrivateFixture(t)
	resource := testResource(t, first)
	second, err := OpenDriver(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	release, err := first.projectLock(resource.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if undo, err := second.projectLock(resource.ProjectID); err == nil {
		if err := undo(); err != nil {
			t.Error(err)
		}
		t.Fatal("跨驱动同时获得同一项目锁")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	undo, err := second.projectLock(resource.ProjectID)
	if err != nil {
		t.Fatal("原锁释放后无法接续")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
}

// TestSamplingFailureDoesNotRefreshOldFacts 外部读取失败后旧事实仍保留旧时间，绝不伪造刚采集的绿色状态。
func TestSamplingFailureDoesNotRefreshOldFacts(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := testResource(t, driver)
	work := execution{driver: driver, lease: Lease{Target: Target{Resource: resource}}}
	if work.observation() != nil {
		t.Fatal("未采样时伪造未创建事实")
	}
	work.sampledAt = time.Now().UTC().Add(-time.Minute)
	work.state.volume = &volumeState{Name: resource.Volume}
	before := work.observation()
	driver.execute = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("受控Docker失联") }
	if err := work.refresh(context.Background()); err == nil {
		t.Fatal("采集失败未传播")
	}
	after := work.observation()
	if before.ObservedAt != after.ObservedAt || !after.VolumeFound {
		t.Fatal("失败采集刷新旧时间或抹掉旧事实")
	}
}

// TestPersistentFencePrecedesExternalEffects 任一检查点未知或被围栏拒绝时，不继续产生Docker动作。
func TestPersistentFencePrecedesExternalEffects(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	lease := Lease{Target: Target{ProjectID: resource.ProjectID, Generation: "1", Desired: "running", Resource: resource}, Operation: Operation{ProjectID: resource.ProjectID, Generation: "1"}, Execution: "1"}
	calls := 0
	driver.execute = func(context.Context, ...string) ([]byte, error) { calls++; return nil, errors.New("不得调用") }
	for _, cause := range []error{nil, errors.New("受控提交结果未知")} {
		_, err := driver.Execute(context.Background(), lease, time.Second, func(context.Context, Checkpoint) (bool, error) { return false, cause })
		if err == nil || calls != 0 {
			t.Fatal("检查点失效后仍调用外部系统")
		}
	}
}

// TestRecoveryClassification 重试耗尽、中断、围栏和事实漂移分别保持其原恢复含义。
func TestRecoveryClassification(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	worker, err := NewWorker(nil, nil, driver, DefaultPolicy(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{Target: Target{Desired: "running"}, Operation: Operation{Attempts: 1}}
	fault := failure("DOCKER_UNAVAILABLE", "受控离线", true, true)
	if value := worker.completion(lease, nil, fault, false); value.State != "waiting_retry" || value.Code != fault.Code || !value.NextAttempt.After(time.Now()) {
		t.Fatal("暂时错误未有界退避")
	}
	lease.Operation.Attempts = 6
	if value := worker.completion(lease, nil, fault, false); value.State != "needs_attention" || value.Code != "RETRY_EXHAUSTED" || value.StepCode != fault.Code {
		t.Fatal("耗尽后仍无界重试或丢失原原因")
	}
	if value := worker.completion(lease, nil, fault, true); value.State != "waiting_retry" || value.Code != "RUNTIME_INTERRUPTED" {
		t.Fatal("控制面退出被报为永久成功或停止")
	}
	lease.Target.Desired = "stopped"
	if value := worker.completion(lease, &Observation{State: "stopped"}, nil, false); value.Code != "STOPPED" {
		t.Fatal("停止与运行结算混淆")
	}
	instance := Instance{Target: Target{Desired: "running"}, OperationState: "succeeded", VolumeConfirmed: true}
	if code := observationCode(instance, &Observation{}, nil); code != "RUNTIME_VOLUME_MISSING" {
		t.Fatal("原卷缺失被当成普通停止")
	}
	if code := observationCode(instance, &Observation{VolumeFound: true, State: "stopped"}, nil); code != "RUNTIME_DRIFT" {
		t.Fatal("外部停止未明确漂移")
	}
	if code := observationCode(instance, nil, fault); code != fault.Code {
		t.Fatal("工具不可用被当成未创建")
	}
	for attempt := 1; attempt <= 8; attempt++ {
		if delay := retryDelay(attempt); delay < time.Second || delay > 30*time.Second {
			t.Fatal("退避超出固定边界")
		}
	}
}
