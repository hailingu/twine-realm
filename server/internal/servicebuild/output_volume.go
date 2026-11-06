// 本文件为二进制提供独立限额临时卷，使暂停后可从daemon核实并提取输出。
package servicebuild

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// outputVolume 只承载本次二进制，不复用MySQL数据卷或任意宿主目录。
type outputVolume struct {
	Name, Driver, CreatedAt string
	Labels, Options         map[string]string
}

// volumeOptions 将大小、所有者和不可执行挂载固定为构建策略内容。
func (box sandbox) volumeOptions() map[string]string {
	environment := box.target.Environment
	return map[string]string{"type": "tmpfs", "device": "tmpfs", "o": "size=" + strconv.FormatInt(box.target.Policy.BinaryBytes, 10) + ",uid=" + strconv.Itoa(environment.UID) + ",gid=" + strconv.Itoa(environment.GID) + ",mode=0700,nosuid,nodev,noexec"}
}

// inspectOutputVolume 通过精确名称和完整归属核实临时卷，陌生卷绝不接管。
func (driver *Driver) inspectOutputVolume(ctx context.Context, box sandbox) (*outputVolume, error) {
	name := box.name + "-out"
	data, err := driver.run(ctx, "volume", "ls", "--filter", "name="+name, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	found, err := listedOutputVolume(data, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	data, err = driver.run(ctx, "volume", "inspect", name)
	if err != nil {
		return nil, err
	}
	var values []outputVolume
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return nil, invalidOutput()
	}
	value := values[0]
	if value.Name != name || value.Driver != "local" || value.CreatedAt == "" || !reflect.DeepEqual(value.Labels, box.labels()) || !reflect.DeepEqual(value.Options, box.volumeOptions()) {
		return nil, ownershipConflict()
	}
	return &value, nil
}

// createOutputVolume 只创建明确新卷，未知创建结果由原操作恢复而不盲重试。
func (driver *Driver) createOutputVolume(ctx context.Context, box sandbox) error {
	existing, err := driver.inspectOutputVolume(ctx, box)
	if err != nil {
		return err
	}
	if existing != nil {
		return ownershipConflict()
	}
	args := []string{"volume", "create", "--driver", "local"}
	for _, key := range sortedKeys(box.labels()) {
		args = append(args, "--label", key+"="+box.labels()[key])
	}
	for _, key := range sortedKeys(box.volumeOptions()) {
		args = append(args, "--opt", key+"="+box.volumeOptions()[key])
	}
	if _, err := driver.run(ctx, append(args, box.name+"-out")...); err != nil {
		return err
	}
	value, err := driver.inspectOutputVolume(ctx, box)
	if err != nil {
		return err
	}
	if value == nil {
		return failure("BUILD_OUTPUT_VOLUME_UNCONFIRMED", "临时输出卷创建尚未确认")
	}
	return nil
}

// cleanupOutputVolume 仅在原容器不存在后移除本次临时卷，未知归属保持原样。
func (driver *Driver) cleanupOutputVolume(ctx context.Context, box sandbox) error {
	value, err := driver.inspectOutputVolume(ctx, box)
	if err != nil || value == nil {
		return err
	}
	container, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return err
	}
	if container != nil {
		return failure("BUILD_OUTPUT_VOLUME_BUSY", "原沙箱尚未确认收回，保留临时输出卷")
	}
	_, err = driver.run(ctx, "volume", "rm", value.Name)
	return err
}

// listedOutputVolume 拒绝重复或畸形的卷列表，不做模糊名字认领。
func listedOutputVolume(data []byte, name string) (bool, error) {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var item struct{ Name string }
		if json.Unmarshal([]byte(line), &item) != nil {
			return false, invalidOutput()
		}
		if item.Name == name {
			if found {
				return false, ownershipConflict()
			}
			found = true
		}
	}
	return found, nil
}
