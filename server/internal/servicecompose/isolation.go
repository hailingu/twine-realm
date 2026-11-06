// 本文件验证容器隔离的实际Docker字段，不把Compose文本本身当作安全生效证明。
package servicecompose

import (
	"reflect"
	"runtime"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// validHostConfig 拒绝主机命名空间、特权、额外设备与被放宽的资源限额。
func validHostConfig(plan servicedeploy.Plan, role string, value containerState) bool {
	h := value.HostConfig
	p := plan.Policy
	if h.Privileged || !h.ReadonlyRootfs || h.Memory != int64(p.MemoryMiB)*1024*1024 || h.MemorySwap != h.Memory || h.NanoCpus != int64(p.CPUs)*1000000000 || h.PidsLimit != int64(p.PIDs) || h.PidMode != "" || h.IpcMode != "private" || h.UTSMode != "" || h.UsernsMode != "" || !validNetworks(plan, role, value) {
		return false
	}
	if len(h.CapAdd) != 0 || !reflect.DeepEqual(h.CapDrop, []string{"ALL"}) || !reflect.DeepEqual(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.Links) != 0 || len(h.VolumesFrom) != 0 || len(h.ExtraHosts) != 0 || len(h.DeviceCgroupRules) != 0 || len(h.Devices) != 0 || len(h.DeviceRequests) != 0 || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || h.LogConfig.Type != "none" || len(h.LogConfig.Config) != 0 {
		return false
	}
	want := "rw,noexec,nosuid,nodev,size=" + strconv.Itoa(p.TmpfsMiB*1024*1024) + ",mode=1777"
	if !reflect.DeepEqual(h.Tmpfs, map[string]string{"/tmp": want}) {
		return false
	}
	ports := map[string][]portBinding{}
	if role == "application" {
		ports["8080/tcp"] = []portBinding{{HostIP: "127.0.0.1", HostPort: strconv.Itoa(plan.Request.HostPort)}}
	}
	if !equalPorts(h.PortBindings, ports) {
		return false
	}
	if value.State.Running && !equalPorts(value.NetworkSettings.Ports, ports) {
		return false
	}
	return true
}

// validNetworks 迁移只连数据库私网，应用另连自己拥有的入口网络；拒绝默认或其他项目网络。
func validNetworks(plan servicedeploy.Plan, role string, value containerState) bool {
	networks := value.NetworkSettings.Networks
	_, private := networks[plan.Database.Network]
	if !private {
		return false
	}
	if role == "migration" {
		return len(networks) == 1 && value.HostConfig.NetworkMode == plan.Database.Network
	}
	_, ingress := networks[plan.Resources.IngressNetwork]
	mode := value.HostConfig.NetworkMode
	return ingress && len(networks) == 2 && (mode == plan.Database.Network || mode == plan.Resources.IngressNetwork)
}

// equalPorts 空端口映射使用同一含义，实际发布必须逐项精确一致。
func equalPorts(actual, expected map[string][]portBinding) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if !reflect.DeepEqual(actual[key], value) {
			return false
		}
	}
	return true
}

// validMounts 每种角色只挂自己的600秘密，允许且核实唯一受限tmpfs；无源码、管理或其他角色挂载。
func validMounts(plan servicedeploy.Plan, role string, files mysqlruntime.BindingFiles, value containerState) bool {
	source, destination := files.Application, "/run/secrets/application_password"
	if role == "migration" {
		source, destination = files.Migration, "/run/secrets/migration_password"
	}
	seen := map[string]bool{}
	for _, mount := range value.Mounts {
		if seen[mount.Destination] {
			return false
		}
		seen[mount.Destination] = true
		if mount.Destination == "/tmp" && mount.Type == "tmpfs" && mount.RW {
			continue
		}
		matching := mount.Source == source || runtime.GOOS == "darwin" && plan.Environment.BindSourceMode == "desktop-host-mnt-v1" && mount.Source == "/host_mnt"+source
		if mount.Destination != destination || mount.Type != "bind" || !matching || mount.RW || mount.Name != "" {
			return false
		}
	}
	return seen[destination] && len(seen) <= 2
}
