// 本文件逐项破坏真实运行应有的隔离边界，任何漂移都必须拒绝接管。
package servicecompose

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// isolatedContainer 构造满足公开运行策略的检查夹具，不把它当成真实Docker验收。
func isolatedContainer(plan servicedeploy.Plan, role string, files mysqlruntime.BindingFiles) containerState {
	v := containerState{ID: strings.Repeat("a", 64), Name: "/" + plan.Resources.Application, Image: plan.Candidate.Image.ID}
	v.Config.Image = plan.Candidate.Image.ID
	v.Config.User = fmt.Sprintf("%d:%d", plan.Environment.UID, plan.Environment.GID)
	v.Config.Entrypoint = []string{"/service"}
	v.Config.Cmd = []string{"serve"}
	v.Config.Labels = labels(plan, role)
	for k, value := range environment(plan, role) {
		v.Config.Env = append(v.Config.Env, k+"="+value)
	}
	v.State.Running, v.State.Status = true, "running"
	v.HostConfig.ReadonlyRootfs = true
	v.HostConfig.Memory = int64(plan.Policy.MemoryMiB) * 1024 * 1024
	v.HostConfig.MemorySwap = v.HostConfig.Memory
	v.HostConfig.NanoCpus = int64(plan.Policy.CPUs) * 1000000000
	v.HostConfig.PidsLimit = int64(plan.Policy.PIDs)
	v.HostConfig.IpcMode = "private"
	v.HostConfig.NetworkMode = plan.Database.Network
	v.HostConfig.CapDrop = []string{"ALL"}
	v.HostConfig.SecurityOpt = []string{"no-new-privileges:true"}
	v.HostConfig.RestartPolicy.Name = "no"
	v.HostConfig.LogConfig.Type = "none"
	v.HostConfig.Tmpfs = map[string]string{"/tmp": fmt.Sprintf("rw,noexec,nosuid,nodev,size=%d,mode=1777", plan.Policy.TmpfsMiB*1024*1024)}
	v.NetworkSettings.Networks = map[string]any{plan.Database.Network: struct{}{}}
	v.HostConfig.PortBindings = map[string][]portBinding{}
	secret, destination := files.Migration, "/run/secrets/migration_password"
	if role == "application" {
		secret, destination = files.Application, "/run/secrets/application_password"
		v.NetworkSettings.Networks[plan.Resources.IngressNetwork] = struct{}{}
		v.HostConfig.PortBindings["8080/tcp"] = []portBinding{{HostIP: "127.0.0.1", HostPort: fmt.Sprint(plan.Request.HostPort)}}
	} else {
		v.Name = "/" + plan.Resources.Migration
		v.Config.Cmd = []string{"migrate"}
	}
	v.NetworkSettings.Ports = v.HostConfig.PortBindings
	v.Mounts = append(v.Mounts, struct {
		Type, Name, Source, Destination string
		RW                              bool
	}{Type: "bind", Source: secret, Destination: destination})
	return v
}

// TestContainerIsolationDrift 应用和迁移的权限、资源、网络、秘密和执行身份都不能由外部放宽。
func TestContainerIsolationDrift(t *testing.T) {
	p := structuralPlan(t)
	files := mysqlruntime.BindingFiles{Application: "/private/app.secret", Migration: "/private/migration.secret"}
	for _, role := range []string{"application", "migration"} {
		if err := validateContainer(p, role, files, isolatedContainer(p, role, files)); err != nil {
			t.Fatal("合法隔离策略被拒绝", err)
		}
		for name, change := range isolationChanges() {
			t.Run(role+"/"+name, func(t *testing.T) {
				value := isolatedContainer(p, role, files)
				change(&value)
				if err := validateContainer(p, role, files, value); err == nil {
					t.Fatal("越界容器被接管")
				}
			})
		}
	}
}

// isolationChanges 每项改变一个可被宿主观察的安全事实，覆盖失败的实际契约。
func isolationChanges() map[string]func(*containerState) {
	return map[string]func(*containerState){
		"镜像替换":     func(v *containerState) { v.Image = "sha256:" + strings.Repeat("f", 64) },
		"用户改root":  func(v *containerState) { v.Config.User = "0:0" },
		"入口改写":     func(v *containerState) { v.Config.Entrypoint = []string{"/bin/sh"} },
		"归属改写":     func(v *containerState) { v.Config.Labels["io.twine-realm.deploy.resource"] = "foreign" },
		"额外秘密变量":   func(v *containerState) { v.Config.Env = append(v.Config.Env, "DB_PASSWORD=synthetic") },
		"特权":       func(v *containerState) { v.HostConfig.Privileged = true },
		"可写根":      func(v *containerState) { v.HostConfig.ReadonlyRootfs = false },
		"无限内存":     func(v *containerState) { v.HostConfig.Memory = 0 },
		"额外swap":   func(v *containerState) { v.HostConfig.MemorySwap = -1 },
		"主机IPC":    func(v *containerState) { v.HostConfig.IpcMode = "host" },
		"主机PID":    func(v *containerState) { v.HostConfig.PidMode = "host" },
		"附加能力":     func(v *containerState) { v.HostConfig.CapAdd = []string{"SYS_ADMIN"} },
		"无提权保护":    func(v *containerState) { v.HostConfig.SecurityOpt = nil },
		"主机路由":     func(v *containerState) { v.HostConfig.ExtraHosts = []string{"host:host-gateway"} },
		"设备":       func(v *containerState) { v.HostConfig.Devices = []any{"/dev/example"} },
		"自动重启迁移":   func(v *containerState) { v.HostConfig.RestartPolicy.Name = "always" },
		"持久敏感日志":   func(v *containerState) { v.HostConfig.LogConfig.Type = "json-file" },
		"可执行tmpfs": func(v *containerState) { v.HostConfig.Tmpfs["/tmp"] = "rw,size=67108864" },
		"外来网络":     func(v *containerState) { v.NetworkSettings.Networks["foreign"] = struct{}{} },
		"缺私网":      func(v *containerState) { v.NetworkSettings.Networks = map[string]any{} },
		"公开端口": func(v *containerState) {
			v.HostConfig.PortBindings["1234/tcp"] = []portBinding{{HostIP: "0.0.0.0", HostPort: "1234"}}
		},
		"实际端口未发布": func(v *containerState) {
			v.NetworkSettings.Ports = map[string][]portBinding{"9999/tcp": {{HostIP: "127.0.0.1", HostPort: "9999"}}}
		},
		"可写秘密":   func(v *containerState) { v.Mounts[0].RW = true },
		"其他角色秘密": func(v *containerState) { v.Mounts[0].Source = "/private/root.secret" },
		"重复挂载":   func(v *containerState) { v.Mounts = append(v.Mounts, v.Mounts[0]) },
		"暂停":     func(v *containerState) { v.State.Paused = true },
		"未知容器状态": func(v *containerState) { v.State.Status = "removing" },
	}
}
