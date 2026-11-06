// 本文件拥有应用入口bridge，数据库与迁移始终留在原internal私网。
package servicecompose

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// ingressNetwork 只读取归属与必要网络配置，不把网络返回内容交给命令执行。
type ingressNetwork struct {
	ID, Name, Driver, Scope          string
	Internal, EnableIPv4, EnableIPv6 bool
	Options, Labels                  map[string]string
	Containers                       map[string]struct{ Name string }
}

// inspectIngress 同名网络必须完整归属本服务，无额外主机路由选项或外来容器。
func (driver *Driver) inspectIngress(ctx context.Context, plan servicedeploy.Plan) (*ingressNetwork, error) {
	data, err := driver.run(ctx, plan, "network", "ls", "--filter", "name="+plan.Resources.IngressNetwork, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	names, err := listedNames(data, "network")
	if err != nil {
		return nil, err
	}
	found, err := exactListedName(names, plan.Resources.IngressNetwork)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	data, err = driver.run(ctx, plan, "network", "inspect", plan.Resources.IngressNetwork)
	if err != nil {
		return nil, err
	}
	var values []ingressNetwork
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return nil, invalidDocker()
	}
	value := values[0]
	if err := validateIngress(plan, value); err != nil {
		return nil, err
	}
	return &value, nil
}

// validateIngress 核实身份、隔离和连接者，不能把外来容器加入的网络视为原空入口。
func validateIngress(plan servicedeploy.Plan, value ingressNetwork) error {
	if !containerID.MatchString(value.ID) || value.Name != plan.Resources.IngressNetwork || value.Driver != "bridge" || value.Scope != "local" || value.Internal || !value.EnableIPv4 || value.EnableIPv6 || len(value.Containers) > 1 {
		return ownershipConflict()
	}
	for key, want := range labels(plan, "ingress") {
		if value.Labels[key] != want {
			return ownershipConflict()
		}
	}
	if !validIngressOptions(value.Options) {
		return ownershipConflict()
	}
	for id, container := range value.Containers {
		if !containerID.MatchString(id) || container.Name != plan.Resources.Application {
			return ownershipConflict()
		}
	}
	return nil
}

// validIngressOptions 仅认可回环缺省发布及Docker记录的固定IP族选项。
func validIngressOptions(options map[string]string) bool {
	if options["com.docker.network.bridge.host_binding_ipv4"] != "127.0.0.1" {
		return false
	}
	expected := map[string]string{"com.docker.network.bridge.host_binding_ipv4": "127.0.0.1", "com.docker.network.enable_ipv4": "true", "com.docker.network.enable_ipv6": "false"}
	for key, value := range options {
		want, present := expected[key]
		if !present || want != value {
			return false
		}
	}
	return true
}

// ensureIngress 仅为已受理目标创建全新入口网络，不接管同名对象；创建未知后重新核实原对象。
func (driver *Driver) ensureIngress(ctx context.Context, plan servicedeploy.Plan) error {
	value, err := driver.inspectIngress(ctx, plan)
	if err != nil || value != nil {
		return err
	}
	args := []string{"network", "create", "--driver", "bridge", "--ipv6=false", "--opt", "com.docker.network.bridge.host_binding_ipv4=127.0.0.1"}
	owned := labels(plan, "ingress")
	keys := []string{}
	for key := range owned {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+owned[key])
	}
	data, err := driver.run(ctx, plan, append(args, plan.Resources.IngressNetwork)...)
	if err != nil {
		return err
	}
	if !containerID.MatchString(strings.TrimSpace(string(data))) {
		return invalidDocker()
	}
	value, err = driver.inspectIngress(ctx, plan)
	if err != nil {
		return err
	}
	if value == nil {
		return servicedeploy.Failure("DEPLOY_NETWORK_RESULT_UNKNOWN", "原入口网络创建结果尚未核实，禁止继续启动", true)
	}
	return nil
}
