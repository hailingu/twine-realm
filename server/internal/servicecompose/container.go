// 本文件在任何启动和恢复前核实实际容器，不凭名字或标签单独接管资源。
package servicecompose

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

var containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// containerState 只解析执行、隔离与身份字段，返回环境和挂载不会被当成下一命令。
type containerState struct {
	ID, Name, Image string
	Config          struct {
		Image, User          string
		Env, Entrypoint, Cmd []string
		Labels               map[string]string
		Healthcheck          *json.RawMessage
	}
	State struct {
		Running, Paused, Restarting, Dead bool
		Status                            string
		ExitCode                          int
		Error                             string
		StartedAt                         string
	}
	HostConfig struct {
		Privileged, ReadonlyRootfs                                                      bool
		Memory, MemorySwap, NanoCpus, PidsLimit                                         int64
		PidMode, IpcMode, UTSMode, UsernsMode, NetworkMode                              string
		PortBindings                                                                    map[string][]portBinding
		Tmpfs                                                                           map[string]string
		CapAdd, CapDrop, SecurityOpt, Links, VolumesFrom, ExtraHosts, DeviceCgroupRules []string
		Devices, DeviceRequests                                                         []any
		RestartPolicy                                                                   struct {
			Name              string
			MaximumRetryCount int
		}
		LogConfig struct {
			Type   string
			Config map[string]string
		}
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]any
		Ports    map[string][]portBinding
	}
}

// portBinding 对宿主IP与端口做精确匹配，空IP不能被当成回环。
type portBinding struct{ HostIP, HostPort string }

// run 固定原本机context及期限，所有参数只来自平台计划。
func (driver *Driver) run(ctx context.Context, plan servicedeploy.Plan, args ...string) ([]byte, error) {
	return localdocker.Run(ctx, localdocker.Policy{Timeout: time.Duration(plan.Policy.CommandSeconds) * time.Second}, append([]string{"--context", plan.Environment.Context}, args...)...)
}

// inspectContainer 同时查同名与同归属对象，改名旧容器存在时禁止启动第二个。
func (driver *Driver) inspectContainer(ctx context.Context, plan servicedeploy.Plan, role string, files mysqlruntime.BindingFiles) (*containerState, error) {
	name := plan.Resources.Application
	if role == "migration" {
		name = plan.Resources.Migration
	}
	found, err := driver.containerLocated(ctx, plan, role, name)
	if err != nil || !found {
		return nil, err
	}
	data, err := driver.run(ctx, plan, "container", "inspect", name)
	if err != nil {
		return nil, err
	}
	var values []containerState
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return nil, invalidDocker()
	}
	if err := validateContainer(plan, role, files, values[0]); err != nil {
		return nil, err
	}
	if role == "application" {
		network, err := driver.inspectIngress(ctx, plan)
		if err != nil {
			return nil, err
		}
		if network == nil {
			return nil, containerConflict("原业务入口网络缺失")
		}
	}
	return &values[0], nil
}

// validateContainer 跨核实镜像、进程、用户、环境、网络和只读用途挂载。
func validateContainer(plan servicedeploy.Plan, role string, files mysqlruntime.BindingFiles, value containerState) error {
	name, command := plan.Resources.Application, "serve"
	if role == "migration" {
		name, command = plan.Resources.Migration, "migrate"
	}
	if !containerID.MatchString(value.ID) || value.Name != "/"+name || value.Image != plan.Candidate.Image.ID || value.Config.Image != plan.Candidate.Image.ID || value.Config.User != fmt.Sprintf("%d:%d", plan.Environment.UID, plan.Environment.GID) || !reflect.DeepEqual(value.Config.Entrypoint, []string{"/service"}) || !reflect.DeepEqual(value.Config.Cmd, []string{command}) || value.Config.Healthcheck != nil {
		return containerConflict("容器执行身份与固定镜像、进程或用户不一致")
	}
	for key, expected := range labels(plan, role) {
		if value.Config.Labels[key] != expected {
			return containerConflict("容器归属标签与原计划不一致")
		}
	}
	if !validEnvironment(value.Config.Env, environment(plan, role)) {
		return containerConflict("容器环境与固定用途配置不一致")
	}
	if !validHostConfig(plan, role, value) {
		return containerConflict("容器权限、网络、端口或资源限制不一致")
	}
	if !validMounts(plan, role, files, value) {
		return containerConflict("容器角色秘密或临时挂载不一致")
	}
	if value.State.Paused || value.State.Restarting || value.State.Dead || !slices.Contains([]string{"created", "running", "exited"}, value.State.Status) {
		return servicedeploy.Failure("DEPLOY_CONTAINER_STATE_UNKNOWN", "原容器状态不能安全接续，保留现场", true)
	}
	return nil
}

// validEnvironment 拒绝额外密码值、迁移变量、代理或重复项。
func validEnvironment(actual []string, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, entry := range actual {
		key, value, ok := strings.Cut(entry, "=")
		want, present := expected[key]
		if !ok || !present || seen[key] || want != value {
			return false
		}
		seen[key] = true
	}
	return true
}

// ownershipConflict 始终保留不符对象，不回显其环境、挂载或业务数据。
func ownershipConflict() error {
	return servicedeploy.Failure("DEPLOY_OWNERSHIP_CONFLICT", "同名、改名或配置不符的业务容器已存在，保留原对象等待核实", false)
}

// containerConflict 只补充固定字段类别，不回显外部配置内容。
func containerConflict(message string) error {
	return servicedeploy.Failure("DEPLOY_OWNERSHIP_CONFLICT", message+"，保留原对象等待核实", false)
}

// invalidDocker 对不完整的外部返回采取未知处理。
func invalidDocker() error {
	return servicedeploy.Failure("DEPLOY_DOCKER_OUTPUT_INVALID", "Docker业务资源返回不完整，须核实原对象", true)
}
