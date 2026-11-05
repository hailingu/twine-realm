// 本文件核实固定容器的关键隔离配置与真正认证就绪，拒绝仅进程或TCP可达的假成功。
package mysqlruntime

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// dockerIdentity 限定 daemon 返回的内容身份，不把任意文本用作后续命令参数。
var dockerIdentity = regexp.MustCompile(`^[0-9a-f]{64}$`)

// mysqlIdentity 对 MySQL 原生 UUID 单独校验，不要求它使用平台实体UUID的生成版本。
var mysqlIdentity = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validateContainer 对归属之外的关键执行、权限、网络、挂载与端口逐项核实。
func (driver *Driver) validateContainer(resource Resource, image imageSpec, value containerState) error {
	if !dockerIdentity.MatchString(value.ID) || value.Name != "/"+resource.Container || value.Image != image.ID || value.Config.Image != Image || !matchingLabels(value.Config.Labels, resource) {
		return containerConflict("容器身份、镜像或归属标签不符")
	}
	if value.Config.User != image.Config.User || !reflect.DeepEqual(value.Config.Entrypoint, image.Config.Entrypoint) || !reflect.DeepEqual(value.Config.Cmd, []string{"mysqld", "--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci"}) {
		return containerConflict("容器用户或启动进程与固定镜像方案不符")
	}
	if !matchingEnvironment(value.Config.Env, image.Config.Env) {
		return containerConflict("容器环境与镜像默认值及秘密文件引用不符")
	}
	if !validHostConfig(resource, value) {
		return containerConflict("容器权限、隔离、内存或重启策略不符")
	}
	if !validContainerNetwork(resource, value) {
		return containerConflict("容器私网或端口发布配置不符")
	}
	if !driver.validMounts(resource, value) {
		return containerConflict("容器数据卷或只读秘密挂载不符")
	}
	health := value.Config.Healthcheck
	if !reflect.DeepEqual(health.Test, []string{"CMD", "mysql", "--defaults-extra-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute=SELECT 1"}) || health.Interval != int64(5*time.Second) || health.Timeout != int64(3*time.Second) || health.StartPeriod != int64(5*time.Second) || health.Retries != 3 {
		return containerConflict("容器认证就绪探针配置不符")
	}
	if value.State.Status != "running" && value.State.Status != "created" && value.State.Status != "exited" && value.State.Status != "restarting" {
		return failure("RUNTIME_STATE_UNKNOWN", "容器处于不可安全接续的状态，保留现场", true, true)
	}
	return nil
}

// containerConflict 仅发布固定诊断分类，不回显外部环境、路径或秘密。
func containerConflict(message string) *Fault {
	return failure("RUNTIME_OWNERSHIP_CONFLICT", message+"，保留原对象并等待核实", false, false)
}

// matchingEnvironment 只允许镜像默认值与文件引用，重复或额外环境值均拒绝。
func matchingEnvironment(actual, baseline []string) bool {
	expected := map[string]string{}
	for _, entry := range baseline {
		key, value, exists := strings.Cut(entry, "=")
		if !exists {
			return false
		}
		expected[key] = value
	}
	expected["MYSQL_ROOT_PASSWORD_FILE"] = "/run/secrets/root_password"
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, entry := range actual {
		key, value, exists := strings.Cut(entry, "=")
		if !exists || seen[key] || expected[key] != value {
			return false
		}
		if _, exists := expected[key]; !exists {
			return false
		}
		seen[key] = true
	}
	return true
}

// validHostConfig 只允许此切片声明的隔离形态，拒绝主机命名空间、特权或额外挂载来源。
func validHostConfig(resource Resource, value containerState) bool {
	config := value.HostConfig
	memory, ok := resource.Configuration["memoryMiB"].(float64)
	if !ok || config.Memory != int64(memory)*1024*1024 || config.Privileged || config.ReadonlyRootfs || config.PidMode != "" || config.UTSMode != "" || config.UsernsMode != "" || config.IpcMode != "private" || config.NetworkMode != resource.Network {
		return false
	}
	if len(config.PortBindings) != 0 || len(config.CapAdd) != 0 || len(config.CapDrop) != 0 || len(config.Devices) != 0 || len(config.DeviceRequests) != 0 || len(config.DeviceCgroupRules) != 0 || len(config.Links) != 0 || len(config.VolumesFrom) != 0 || len(config.ExtraHosts) != 0 {
		return false
	}
	return config.RestartPolicy.Name == "unless-stopped" && config.RestartPolicy.MaximumRetryCount == 0 && reflect.DeepEqual(config.SecurityOpt, []string{"no-new-privileges:true"})
}

// validContainerNetwork 私网必须是唯一网络，3306和其他容器端口都不得发布到宿主。
func validContainerNetwork(resource Resource, value containerState) bool {
	if len(value.NetworkSettings.Networks) != 1 {
		return false
	}
	if _, exists := value.NetworkSettings.Networks[resource.Network]; !exists {
		return false
	}
	for _, bindings := range value.NetworkSettings.Ports {
		if bindings != nil {
			return false
		}
	}
	return true
}

// validMounts 只接受固定数据卷和两个只读秘密文件，不挂载仓库、主目录或Docker socket。
func (driver *Driver) validMounts(resource Resource, value containerState) bool {
	if len(value.Mounts) != 3 {
		return false
	}
	prefix := filepath.Join(driver.directory, "projects", resource.ID)
	expected := map[string]string{"/run/secrets/root_password": filepath.Join(prefix, "root.secret"), "/run/secrets/client_options": filepath.Join(prefix, "client.secret")}
	dataFound := false
	for _, mount := range value.Mounts {
		if mount.Destination == "/var/lib/mysql" {
			if dataFound || mount.Type != "volume" || mount.Name != resource.Volume || !mount.RW {
				return false
			}
			dataFound = true
			continue
		}
		source, exists := expected[mount.Destination]
		if !exists || mount.Type != "bind" || !driver.matchingBindSource(mount.Source, source) || mount.RW {
			return false
		}
		delete(expected, mount.Destination)
	}
	return dataFound && len(expected) == 0
}

// matchingBindSource 只在已核实的Mac Docker Desktop中识别其固定VM前缀，路径其余部分必须逐字节一致。
func (driver *Driver) matchingBindSource(actual, expected string) bool {
	if actual == expected {
		return true
	}
	return runtime.GOOS == "darwin" && driver.Environment().BindSourceMode == "desktop-host-mnt-v1" && actual == "/host_mnt"+expected
}

// probe 等待最终mysqld成为PID1后认证查询，避免初始化临时server被误报为已就绪。
func (driver *Driver) probe(ctx context.Context, resource Resource, containerID string) (string, error) {
	if err := driver.verifySecrets(resource); err != nil {
		return "", err
	}
	data, err := driver.execute(ctx, contextOption, driver.Environment().Context, "exec", containerID, "cat", "/proc/1/comm")
	if err != nil || strings.TrimSpace(string(data)) != "mysqld" {
		return "", failure("MYSQL_NOT_READY", "最终MySQL进程尚未就绪，保留初始化现场", true, false)
	}
	data, err = driver.execute(ctx, contextOption, driver.Environment().Context, "exec", containerID, "mysql", "--defaults-extra-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute=SELECT @@server_uuid, VERSION()")
	if err != nil {
		return "", failure("MYSQL_NOT_READY", "MySQL认证查询尚未完成，不能报告就绪", true, false)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || !mysqlIdentity.MatchString(fields[0]) || fields[1] != "9.7.4" {
		return "", failure("MYSQL_IDENTITY_CONFLICT", "数据库身份或版本与固定运行方案不符", false, false)
	}
	return fields[0], nil
}
