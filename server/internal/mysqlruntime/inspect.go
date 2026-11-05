// 本文件把 Docker 返回值转换为有限事实，严格核实归属、镜像、挂载和网络。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"strings"
)

// networkState 仅接收核实私网所需字段。
type networkState struct {
	ID, Name, Driver, Scope string
	Internal                bool
	EnableIPv4, EnableIPv6  bool
	Labels                  map[string]string
	Options                 map[string]string
}

// volumeState 不暴露 Docker 数据目录，只核实固定卷的归属和驱动。
type volumeState struct {
	Name, Driver string
	Labels       map[string]string
	Options      map[string]string
}

// containerState 将检查字段限制在可信计划边界，不执行返回值中的命令。
type containerState struct {
	ID, Name, Image string
	Config          struct {
		Image, User          string
		Env, Entrypoint, Cmd []string
		Labels               map[string]string
		Healthcheck          struct {
			Test                           []string
			Interval, Timeout, StartPeriod int64
			Retries                        int
		}
	}
	State struct {
		Running   bool
		Status    string
		StartedAt string
	}
	HostConfig struct {
		Memory                                                                                 int64
		Privileged, ReadonlyRootfs                                                             bool
		PortBindings                                                                           map[string]any
		Binds, CapAdd, CapDrop, SecurityOpt, Links, VolumesFrom, DeviceCgroupRules, ExtraHosts []string
		Devices, DeviceRequests                                                                []any
		PidMode, IpcMode, UTSMode, UsernsMode, NetworkMode                                     string
		RestartPolicy                                                                          struct {
			Name              string
			MaximumRetryCount int
		}
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]any
		Ports    map[string]any
	}
}

// inspected 保存一次核实所见的资源，不把空指针当 Docker 离线。
type inspected struct {
	network   *networkState
	volume    *volumeState
	container *containerState
}

// findNamed 先有界查询再精确匹配名字，未找到与工具失败分别处理。
func (driver *Driver) findNamed(ctx context.Context, kind, name string, target any) (bool, error) {
	args := []string{contextOption, driver.Environment().Context, kind, "ls", "--filter", "name=" + name, "--format", "{{json .}}"}
	if kind == "container" {
		args = append(args, "--all")
	}
	data, err := driver.execute(ctx, args...)
	if err != nil {
		return false, err
	}
	found, err := listedName(data, name)
	if err != nil || !found {
		return false, err
	}
	data, err = driver.execute(ctx, contextOption, driver.Environment().Context, kind, "inspect", name)
	if err != nil {
		return false, err
	}
	var rows []json.RawMessage
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 || json.Unmarshal(rows[0], target) != nil {
		return false, failure("DOCKER_OUTPUT_INVALID", "Docker资源核实结果无效", false, false)
	}
	return true, nil
}

// listedName 只承认列表中的精确名称，重复与畸形结果不能授权接管。
func listedName(data []byte, name string) (bool, error) {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct{ Name, Names string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return false, failure("DOCKER_OUTPUT_INVALID", "Docker资源列表格式无效", false, false)
		}
		if row.Name == name || row.Names == name {
			if found {
				return false, failure("RUNTIME_RESOURCE_CONFLICT", "同名资源不唯一，原对象保留", false, false)
			}
			found = true
		}
	}
	return found, nil
}

// inspect 核实固定名字和标签，不借名称前缀或空列表擅自认领外部对象。
func (driver *Driver) inspect(ctx context.Context, resource Resource, image imageSpec) (inspected, error) {
	var result inspected
	var network networkState
	found, err := driver.findNamed(ctx, "network", resource.Network, &network)
	if err != nil {
		return result, err
	}
	if found {
		if !validNetworkState(resource, network) {
			return result, ownershipConflict()
		}
		result.network = &network
	}
	var volume volumeState
	found, err = driver.findNamed(ctx, "volume", resource.Volume, &volume)
	if err != nil {
		return result, err
	}
	if found {
		if !matchingLabels(volume.Labels, resource) || volume.Name != resource.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
			return result, ownershipConflict()
		}
		result.volume = &volume
	}
	if err := driver.checkRenamedContainer(ctx, resource); err != nil {
		return result, err
	}
	var container containerState
	found, err = driver.findNamed(ctx, "container", resource.Container, &container)
	if err != nil {
		return result, err
	}
	if found {
		if err := driver.validateContainer(resource, image, container); err != nil {
			return result, err
		}
		result.container = &container
	}
	return result, nil
}

// validNetworkState 识别Docker写入的固定IP族选项，其他驱动选项和不符归属仍拒绝。
func validNetworkState(resource Resource, value networkState) bool {
	if !matchingLabels(value.Labels, resource) || value.Name != resource.Network || value.Driver != "bridge" || !value.Internal || value.Scope != "local" || !value.EnableIPv4 || value.EnableIPv6 {
		return false
	}
	for key, actual := range value.Options {
		switch key {
		case "com.docker.network.enable_ipv4":
			if actual != "true" {
				return false
			}
		case "com.docker.network.enable_ipv6":
			if actual != "false" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// checkRenamedContainer 不在原容器被外部改名后启动第二个写同一卷的数据库。
func (driver *Driver) checkRenamedContainer(ctx context.Context, resource Resource) error {
	data, err := driver.execute(ctx, contextOption, driver.Environment().Context, "container", "ls", "--all", "--filter", "label=io.twine-realm.resource="+resource.ID, "--format", "{{json .}}")
	if err != nil {
		return err
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		count++
		var row struct{ Names string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return failure("DOCKER_OUTPUT_INVALID", "容器归属列表格式无效", false, false)
		}
		if count > 1 || row.Names != resource.Container {
			return ownershipConflict()
		}
	}
	return nil
}

// matchingLabels 所有受控标签须精确匹配，Compose 自身的附加标签不获得额外授权。
func matchingLabels(actual map[string]string, resource Resource) bool {
	for key, value := range labels(resource) {
		if actual[key] != value {
			return false
		}
	}
	return true
}

// ownershipConflict 不回显未知资源的环境变量、挂载路径或任何凭据。
func ownershipConflict() *Fault {
	return failure("RUNTIME_OWNERSHIP_CONFLICT", "发现同名、改名或配置不符的资源，保留原对象并等待核实", false, false)
}
