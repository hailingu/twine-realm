// 本文件只解析受控Docker名单，列表、精确名称和完整对象检查各自独立。
package servicecompose

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

const dockerFilter = "--filter"

// listedNames 不执行名单内容；容器与网络仅读取其固定名称字段。
func listedNames(data []byte, kind string) ([]string, error) {
	names := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct{ Names, Name string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return nil, invalidDocker()
		}
		name := row.Names
		if kind == "network" {
			name = row.Name
		}
		if name == "" || len(names) >= 500 {
			return nil, invalidDocker()
		}
		names = append(names, name)
	}
	return names, nil
}

// exactListedName 名称过滤允许前缀候选，但不能把候选或重复行当成唯一原对象。
func exactListedName(names []string, expected string) (bool, error) {
	count := 0
	for _, name := range names {
		if name == expected {
			count++
		}
	}
	if count > 1 {
		return false, ownershipConflict()
	}
	return count == 1, nil
}

// containerLocated 同时核对标签名单和同名名单，改名或两个归属对象均阻断。
func (driver *Driver) containerLocated(ctx context.Context, plan servicedeploy.Plan, role, name string) (bool, error) {
	args := []string{"container", "ls", "--all", dockerFilter, "label=io.twine-realm.deploy.installation=" + plan.Environment.RuntimeInstallationID, dockerFilter, "label=io.twine-realm.deploy.project=" + plan.ProjectID, dockerFilter, "label=io.twine-realm.deploy.service=" + plan.ServiceID, dockerFilter, "label=io.twine-realm.deploy.role=" + role, "--format", "{{json .}}"}
	data, err := driver.run(ctx, plan, args...)
	if err != nil {
		return false, err
	}
	owned, err := listedNames(data, "container")
	if err != nil {
		return false, err
	}
	if len(owned) > 1 || len(owned) == 1 && owned[0] != name {
		return false, ownershipConflict()
	}
	data, err = driver.run(ctx, plan, "container", "ls", "--all", dockerFilter, "name="+name, "--format", "{{json .}}")
	if err != nil {
		return false, err
	}
	names, err := listedNames(data, "container")
	if err != nil {
		return false, err
	}
	found, err := exactListedName(names, name)
	if err != nil {
		return false, err
	}
	if !found && len(owned) != 0 {
		return false, ownershipConflict()
	}
	return found, nil
}
