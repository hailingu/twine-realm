// 本文件使用真实沙箱已观测的形态验证外来资源拒绝；成功执行另由Docker集成检查证明。
package servicebuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sandboxFixture 保存公开元数据形态，不创建容器、卷或宿主挂载。
func sandboxFixture(t *testing.T) (*Driver, sandbox, containerState) {
	t.Helper()
	lease, _ := validMetadata(t)
	path := filepath.Join(t.TempDir(), "private-build")
	if err := InitializeDirectory(path, nil, lease.Target.Environment); err != nil {
		t.Fatal(err)
	}
	driver, err := OpenDriver(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	lease.Target.InstallationID = driver.InstallationID()
	box := driver.newSandbox(lease.Target, lease.Operation.ID)
	var value containerState
	value.ID, value.Name, value.Image = strings.Repeat("a", 64), "/"+box.name, lease.Target.Environment.ImageID
	value.Config.Image, value.Config.User, value.Config.WorkingDir = value.Image, fmt.Sprintf("%d:%d", lease.Target.Environment.UID, lease.Target.Environment.GID), sourceMountPath
	value.Config.Entrypoint, value.Config.Cmd, value.Config.Labels = []string{"/usr/bin/tail"}, []string{"-f", "/dev/null"}, box.labels()
	value.Config.Env = []string{"PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "GOLANG_VERSION=1.27.3"}
	for _, key := range sortedKeys(sandboxEnvironment()) {
		value.Config.Env = append(value.Config.Env, key+"="+sandboxEnvironment()[key])
	}
	value.State.Running, value.State.Status = true, "running"
	config := &value.HostConfig
	config.Memory, config.MemorySwap, config.NanoCpus, config.PidsLimit = 4294967296, 4294967296, 2000000000, 256
	config.ReadonlyRootfs, config.IpcMode, config.NetworkMode = true, "private", "bridge"
	config.CapDrop, config.SecurityOpt, config.RestartPolicy.Name = []string{"ALL"}, []string{"no-new-privileges=true"}, "no"
	config.Tmpfs = map[string]string{"/tmp": "rw,exec,nosuid,nodev,size=3221225472"}
	value.Mounts = []sandboxMount{{Type: "bind", Source: filepath.Join(box.directory, "source"), Destination: sourceMountPath}, {Type: "volume", Name: box.name + "-out", Destination: "/output", RW: true}}
	value.NetworkSettings.Networks = map[string]any{"bridge": map[string]any{}}
	return driver, box, value
}

// cloneSandbox 为每个故障场景提供独立深拷贝，不交叉污染其他验证。
func cloneSandbox(t *testing.T, value containerState) containerState {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy containerState
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

// TestForeignSandboxConfigurationRejected 每种额外权限、来源或身份都不能授权执行/清理。
func TestForeignSandboxConfigurationRejected(t *testing.T) {
	_, box, baseline := sandboxFixture(t)
	if !validSandbox(baseline, box) || len(box.labels()) != 4 {
		t.Fatal("真实成功形态或固定标签集合不符")
	}
	changes := []func(*containerState){
		func(v *containerState) { v.Name = "/foreign" }, func(v *containerState) { v.ID = "short" }, func(v *containerState) { v.Image = ContentDigest(nil) },
		func(v *containerState) { v.Config.Labels["io.twine-realm.build.installation"] = "foreign" }, func(v *containerState) { v.Config.User = "0" }, func(v *containerState) { v.Config.Cmd = []string{"shell"} },
		func(v *containerState) {
			v.Config.Env = append(v.Config.Env, "TWINE_DATABASE_DSN=synthetic-private-value")
		}, func(v *containerState) { v.HostConfig.Privileged = true }, func(v *containerState) { v.HostConfig.Memory = 0 },
		func(v *containerState) { v.HostConfig.CapAdd = []string{"SYS_ADMIN"} }, func(v *containerState) { v.HostConfig.SecurityOpt = nil }, func(v *containerState) { v.HostConfig.PortBindings = map[string]any{"8080/tcp": []any{}} },
		func(v *containerState) { v.Mounts[0].RW = true }, func(v *containerState) { v.Mounts[0].Source = "/user-repository" }, func(v *containerState) { v.Mounts[1].Name = "user-mysql-data" },
		func(v *containerState) {
			v.Mounts = append(v.Mounts, sandboxMount{Type: "bind", Source: "/var/run/docker.sock", Destination: "/docker.sock", RW: true})
		},
		func(v *containerState) { v.Mounts[1].Destination = sourceMountPath }, func(v *containerState) { v.NetworkSettings.Networks = map[string]any{"host": true} },
		func(v *containerState) { v.NetworkSettings.Ports = map[string]any{"8080/tcp": true} }, func(v *containerState) { v.State.Status = "unknown" },
	}
	for index, change := range changes {
		value := cloneSandbox(t, baseline)
		change(&value)
		if validSandbox(value, box) {
			t.Fatalf("外来资源形态被接受：%d", index)
		}
	}
	if validSandboxEnv([]string{"PATH=wrong"}) {
		t.Fatal("不完整环境被放行")
	}
}

// sandboxReads 只模拟当前操作的只读列表与inspect，任何未声明命令都失败。
func sandboxReads(driver *Driver, box sandbox, value containerState, mode string, mutations *int) dockerCommand {
	return func(ctx context.Context, timeout time.Duration, metadata bool, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "--context local-test info --format {{json .ID}}" {
			return json.Marshal(driver.Environment().DaemonID)
		}
		if len(args) < 4 || args[2] != "container" {
			return fakeEnvironmentCommand(ctx, timeout, metadata, args...)
		}
		if args[3] == "ls" {
			return sandboxList(box, mode, args)
		}
		if args[3] == "inspect" {
			if mode == "broken" {
				return []byte("{}"), nil
			}
			if mode == "error" {
				return nil, errors.New("受控读取失败")
			}
			return json.Marshal([]containerState{value})
		}
		*mutations++
		return nil, errors.New("不应对外来资源执行变更")
	}
}

// sandboxList 模拟标签改名与同名无归属，不能仅凭任一名单授权接管。
func sandboxList(box sandbox, mode string, args []string) ([]byte, error) {
	if mode == "renamed" {
		return json.Marshal(map[string]string{"Names": "foreign-name"})
	}
	if mode == "duplicate" {
		data, _ := json.Marshal(map[string]string{"Names": box.name})
		return []byte(string(data) + "\n" + string(data)), nil
	}
	if mode == "unowned" && strings.Contains(strings.Join(args, " "), "label=") {
		return nil, nil
	}
	return json.Marshal(map[string]string{"Names": box.name})
}

// TestForeignCleanupHasNoMutations 真实归属规则拒绝外来对象后，停止和删除端口均不得被调用。
func TestForeignCleanupHasNoMutations(t *testing.T) {
	for _, mode := range []string{"foreign", "renamed", "duplicate", "unowned", "broken", "error"} {
		t.Run(mode, func(t *testing.T) {
			driver, box, value := sandboxFixture(t)
			if mode == "foreign" {
				value.Config.Labels["io.twine-realm.build.target"] = "foreign"
			}
			mutations := 0
			driver.execute = sandboxReads(driver, box, value, mode, &mutations)
			if err := driver.cleanupSandbox(context.Background(), box); err == nil || mutations != 0 {
				t.Fatal("外来对象被清理或错误被降级")
			}
		})
	}
}
