// 本文件验证拒绝外部畸形结果、环境漂移、旧卷缺失和认证失败；真实运行另有Compose验收。
package mysqlruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResourceLookupRequiresUnambiguousIdentity 名称过滤结果不是归属证据，重复、畸形和非唯一inspect全部拒绝。
func TestResourceLookupRequiresUnambiguousIdentity(t *testing.T) {
	for _, scenario := range []struct {
		data           string
		found, invalid bool
	}{
		{"", false, false}, {`{"Name":"prefix-db"}`, false, false}, {`{"Name":"db"}`, true, false}, {"{\"Name\":\"db\"}\n{\"Name\":\"db\"}", false, true}, {"not-json", false, true},
	} {
		found, err := listedName([]byte(scenario.data), "db")
		if (err != nil) != scenario.invalid || found != scenario.found {
			t.Fatal("名单精确性或畸形结果校验失效")
		}
	}
	driver, _ := openPrivateFixture(t)
	for _, data := range []string{"[]", "[{},{}]", "invalid"} {
		driver.execute = func(_ context.Context, args ...string) ([]byte, error) {
			if contains(args, "ls") {
				return []byte(`{"Name":"db"}`), nil
			}
			return []byte(data), nil
		}
		if _, err := driver.findNamed(context.Background(), "network", "db", &networkState{}); err == nil {
			t.Fatal("非唯一核实结果被接受")
		}
	}
}

// TestPreflightRefusesEnvironmentReplacement 同一context名称不能授权另一daemon或镜像内容接管原数据。
func TestPreflightRefusesEnvironmentReplacement(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	for _, overrides := range []map[string]string{
		{"--context local info --format {{json .}}": `{"ID":"another-daemon","OSType":"linux"}`},
		{"--context local image inspect " + Image: `[{"Id":"sha256:` + strings.Repeat("b", 64) + `","RepoDigests":["mysql@` + strings.Split(Image, "@")[1] + `"]}]`},
	} {
		driver.execute = discoveryFixture(overrides)
		_, err := driver.preflight(context.Background(), resource)
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "DOCKER_IDENTITY_CONFLICT" {
			t.Fatal("环境身份改变未阻断执行")
		}
	}
}

// missingResourcesFixture 只允许真实发现协议和空资源名单，任何创建调用都失败。
func missingResourcesFixture() dockerCommand {
	discovery := discoveryFixture(nil)
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if contains(args, "ls") {
			return []byte{}, nil
		}
		return discovery(ctx, args...)
	}
}

// TestMissingConfirmedVolumeNeverBecomesEmptyDatabase 原卷缺失必须阻断，读取事实也不能生成新秘密。
func TestMissingConfirmedVolumeNeverBecomesEmptyDatabase(t *testing.T) {
	driver, path := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	driver.execute = missingResourcesFixture()
	work := execution{driver: driver, lease: Lease{Target: Target{Resource: resource}, VolumeConfirmed: true}}
	_, err := work.preflight(context.Background())
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "RUNTIME_VOLUME_MISSING" {
		t.Fatal("原卷缺失后允许生成空数据库")
	}
	fact, err := driver.Inspect(context.Background(), resource)
	if err != nil || fact == nil || fact.State != "not_created" || fact.VolumeFound {
		t.Fatal("只读空资源事实被猜成离线或已有数据")
	}
	if _, err := os.Stat(filepath.Join(path, "projects", resource.ID)); !os.IsNotExist(err) {
		t.Fatal("只读采集生成了秘密或项目目录")
	}
}

// TestRetryCannotUndoExternalStop 旧操作重试不启动外部已停止容器，新明确目标或原未启动容器可接续。
func TestRetryCannotUndoExternalStop(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	_, container := containerFixture(t, driver, resource)
	container.State.Running = false
	container.State.Status = "exited"
	calls := 0
	driver.execute = func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if !contains(args, "start") || !contains(args, container.ID) {
			t.Fatal("启动没有使用已核实ID")
		}
		return []byte{}, nil
	}
	work := execution{driver: driver, state: inspected{container: &container}, lease: Lease{Operation: Operation{Attempts: 2}}}
	if err := work.startExisting(context.Background()); err == nil || calls != 0 {
		t.Fatal("旧重试覆盖了外部停止")
	}
	work.lease.Operation.Attempts = 1
	if err := work.startExisting(context.Background()); err != nil || calls != 1 {
		t.Fatal("新明确目标无法恢复原容器")
	}
	work.lease.Operation.Attempts = 2
	container.State.Status = "created"
	if err := work.startExisting(context.Background()); err != nil || calls != 2 {
		t.Fatal("未启动的原容器无法接续")
	}
	container.State.Status = "restarting"
	if err := work.startExisting(context.Background()); err != nil || calls != 2 {
		t.Fatal("重启中的容器重复执行启动")
	}
}

// TestAuthenticationFailureAndIdentityMismatchStayUnready 认证错误可等待，错误数据库身份不能变成绿色事实。
func TestAuthenticationFailureAndIdentityMismatchStayUnready(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	if err := driver.prepareSecrets(resource, false); err != nil {
		t.Fatal(err)
	}
	_, container := containerFixture(t, driver, resource)
	work := execution{driver: driver, state: inspected{container: &container}, lease: Lease{Target: Target{Resource: resource}}}
	result := ""
	rejected := true
	driver.execute = func(_ context.Context, args ...string) ([]byte, error) {
		if contains(args, "cat") {
			return []byte("mysqld\n"), nil
		}
		if rejected {
			return nil, errors.New("受控认证失败")
		}
		return []byte(result), nil
	}
	fact := &Observation{State: "running", Running: true, VolumeFound: true, ObservedAt: Timestamp(time.Now())}
	if ready, err := work.authenticate(context.Background(), fact); err != nil || ready || fact.Ready {
		t.Fatal("认证失败仍呈现就绪")
	}
	rejected = false
	result = resource.ID + "\twrong-version\n"
	if ready, err := work.authenticate(context.Background(), fact); err == nil || ready {
		t.Fatal("错误数据库版本被接受")
	}
	result = resource.ID + "\t9.7.4\n"
	work.lease.ServerUUID = "00000000-0000-4000-8000-000000000099"
	if ready, err := work.authenticate(context.Background(), fact); err == nil || ready || fact.Ready || fact.State != "drifted" {
		t.Fatal("数据库身份替换未报告漂移")
	}
}

// TestCanonicalPathCannotIntroduceInterpolation 父目录别名解析后引入插值字符仍拒绝，不创建运行根。
func TestCanonicalPathCannotIntroduceInterpolation(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "private$parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := newDirectoryPath(filepath.Join(alias, "runtime"), nil); err == nil {
		t.Fatal("规范路径插值字符被接受")
	}
	if _, err := os.Stat(filepath.Join(parent, "runtime")); !os.IsNotExist(err) {
		t.Fatal("拒绝路径时仍创建了目录")
	}
}
