// 本文件严格核实沙箱的资源、权限、挂载和网络，不接管同名或改名的对象。
package servicebuild

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// containerState 只接收决定执行与清理权限的字段，原环境不进入响应。
type containerState struct {
	ID, Name, Image string
	Config          struct {
		Image, User, WorkingDir string
		Env, Entrypoint, Cmd    []string
		Labels                  map[string]string
	}
	State struct {
		Running, Paused bool
		Status          string
	}
	HostConfig struct {
		Memory, MemorySwap, NanoCpus                                                           int64
		PidsLimit                                                                              int
		Privileged, ReadonlyRootfs                                                             bool
		Binds, CapAdd, CapDrop, SecurityOpt, Links, VolumesFrom, DeviceCgroupRules, ExtraHosts []string
		Devices, DeviceRequests                                                                []any
		PortBindings                                                                           map[string]any
		Tmpfs                                                                                  map[string]string
		PidMode, IpcMode, UTSMode, UsernsMode, NetworkMode                                     string
		RestartPolicy                                                                          struct {
			Name              string
			MaximumRetryCount int
		}
	}
	Mounts          []sandboxMount
	NetworkSettings struct {
		Networks map[string]any
		Ports    map[string]any
	}
}

// sandboxMount 是Docker返回的单一挂载，不把任意路径变成操作许可。
type sandboxMount struct {
	Type, Name, Source, Destination string
	RW                              bool
}

// inspectSandbox 搜索本安装操作标签及名字两条线索，重命名也不能启动第二个同目标沙箱。
func (driver *Driver) inspectSandbox(ctx context.Context, box sandbox) (*containerState, error) {
	data, err := driver.run(ctx, "container", "ls", "--all", "--filter", "label=io.twine-realm.build.operation="+box.operationID, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	owned, err := listedSandbox(data, box.name, true)
	if err != nil {
		return nil, err
	}
	data, err = driver.run(ctx, "container", "ls", "--all", "--filter", "name="+box.name, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	found, err := listedSandbox(data, box.name, false)
	if err != nil {
		return nil, err
	}
	if owned != found {
		return nil, ownershipConflict()
	}
	if !found {
		return nil, nil
	}
	data, err = driver.run(ctx, "container", "inspect", box.name)
	if err != nil {
		return nil, err
	}
	var values []containerState
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return nil, invalidOutput()
	}
	if !validSandbox(values[0], box) {
		return nil, ownershipConflict()
	}
	return &values[0], nil
}

// listedSandbox 不允许同操作多对象，模糊名称匹配不会授权任何动作。
func listedSandbox(data []byte, expected string, exclusive bool) (bool, error) {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct{ Names string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return false, invalidOutput()
		}
		if exclusive && row.Names != expected {
			return false, ownershipConflict()
		}
		if row.Names == expected {
			if found {
				return false, ownershipConflict()
			}
			found = true
		}
	}
	return found, nil
}

// validSandbox 同时核对身份、只读源码和资源约束，不因容器已停止而放宽。
func validSandbox(value containerState, box sandbox) bool {
	environment := box.target.Environment
	if !containerIDPattern.MatchString(value.ID) || value.Name != "/"+box.name || value.Image != environment.ImageID || value.Config.Image != environment.ImageID || !reflect.DeepEqual(value.Config.Labels, box.labels()) {
		return false
	}
	if value.Config.User != strconv.Itoa(environment.UID)+":"+strconv.Itoa(environment.GID) || value.Config.WorkingDir != sourceMountPath || !reflect.DeepEqual(value.Config.Entrypoint, []string{"/usr/bin/tail"}) || !reflect.DeepEqual(value.Config.Cmd, []string{"-f", "/dev/null"}) || !validSandboxEnv(value.Config.Env) {
		return false
	}
	if !validSandboxHost(value, box.target.Policy) || len(value.Mounts) != 2 {
		return false
	}
	if !validSandboxMounts(value, box) {
		return false
	}
	if len(value.NetworkSettings.Ports) != 0 || len(value.NetworkSettings.Networks) > 1 {
		return false
	}
	for name := range value.NetworkSettings.Networks {
		if name != "bridge" {
			return false
		}
	}
	return hasExact([]string{"created", "running", "paused", "exited"}, value.State.Status)
}

// validSandboxMounts 只允许固定源码只读挂载与本次限额输出卷，不挂载宿主可写目录。
func validSandboxMounts(value containerState, box sandbox) bool {
	seen := map[string]bool{}
	for _, mount := range value.Mounts {
		if seen[mount.Destination] {
			return false
		}
		seen[mount.Destination] = true
		switch mount.Destination {
		case sourceMountPath:
			if !validSourceMount(mount, box) {
				return false
			}
		case "/output":
			if mount.Type != "volume" || mount.Name != box.name+"-out" || !mount.RW {
				return false
			}
		default:
			return false
		}
	}
	return seen[sourceMountPath] && seen["/output"]
}

// validSandboxEnv 只允许固定工具镜像默认值与明确的非秘密执行环境。
func validSandboxEnv(values []string) bool {
	expected := sandboxEnvironment()
	expected["PATH"] = "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	expected["GOLANG_VERSION"] = "1.27.3"
	if len(values) != len(expected) {
		return false
	}
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		wanted, exists := expected[key]
		if !ok || !exists || value != wanted {
			return false
		}
		delete(expected, key)
	}
	return len(expected) == 0
}

// validSandboxHost 明确拒绝额外设备、能力、端口与主机命名空间。
func validSandboxHost(value containerState, policy Policy) bool {
	config := value.HostConfig
	memory := int64(policy.MemoryMiB) * 1024 * 1024
	if config.Memory != memory || config.MemorySwap != memory || config.NanoCpus != int64(policy.CPUs)*1e9 || config.PidsLimit != policy.Pids || config.Privileged || !config.ReadonlyRootfs || config.PidMode != "" || config.IpcMode != "private" || config.UTSMode != "" || config.UsernsMode != "" || config.NetworkMode != "bridge" {
		return false
	}
	if len(config.Binds)+len(config.CapAdd)+len(config.Links)+len(config.VolumesFrom)+len(config.DeviceCgroupRules)+len(config.ExtraHosts)+len(config.Devices)+len(config.DeviceRequests)+len(config.PortBindings) != 0 {
		return false
	}
	if !reflect.DeepEqual(config.CapDrop, []string{"ALL"}) || !reflect.DeepEqual(config.SecurityOpt, []string{"no-new-privileges=true"}) || config.RestartPolicy.Name != "no" || config.RestartPolicy.MaximumRetryCount != 0 {
		return false
	}
	return len(config.Tmpfs) == 1 && config.Tmpfs["/tmp"] == "rw,exec,nosuid,nodev,size="+strconv.Itoa(policy.TmpfsMiB*1024*1024)
}

// ownershipConflict 不回显外来对象的挂载、环境或潜在秘密。
func ownershipConflict() *Fault {
	return failure("BUILD_OWNERSHIP_CONFLICT", "沙箱归属或隔离配置不符，保留原对象并等待核实")
}

// validSourceMount 仅在已核实Desktop环境识别固定VM前缀，其他路径必须逐字节一致。
func validSourceMount(mount sandboxMount, box sandbox) bool {
	expected := filepath.Join(box.directory, "source")
	matching := mount.Source == expected || box.target.Environment.BindSourceMode == "desktop-host-mnt-v1" && mount.Source == "/host_mnt"+expected
	return mount.Type == "bind" && !mount.RW && matching
}
