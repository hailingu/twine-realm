// 本文件仅渲染受控部署计划；不读取用户Dockerfile、Compose或环境文件。
package servicecompose

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// labels 绑定原安装、服务、运行内容和角色，名字或Compose标签不能单独授权接管。
func labels(plan servicedeploy.Plan, role string) map[string]string {
	return map[string]string{"io.twine-realm.deploy.installation": plan.Environment.RuntimeInstallationID, "io.twine-realm.deploy.project": plan.ProjectID, "io.twine-realm.deploy.service": plan.ServiceID, "io.twine-realm.deploy.resource": plan.Database.ResourceID, "io.twine-realm.deploy.runtime": plan.RuntimeDigest, "io.twine-realm.deploy.role": role}
}

// environment 仅包含已冻结的非敏感定位和本角色文件引用，应用没有迁移变量。
func environment(plan servicedeploy.Plan, role string) map[string]string {
	value := map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HTTP_ADDRESS": "0.0.0.0:8080", "SERVICE_NAME": plan.ServiceName, "DB_HOST": plan.Database.Container, "DB_PORT": "3306", "DB_NAME": plan.Binding.DatabaseName}
	if role == "application" {
		value["DB_USER"], value["DB_PASSWORD_FILE"] = plan.Resources.ApplicationUser, "/run/secrets/application_password"
	} else {
		value["MIGRATION_USER"], value["MIGRATION_PASSWORD_FILE"] = plan.Resources.MigrationUser, "/run/secrets/migration_password"
	}
	return value
}

// composeService 分别隔离应用与迁移，只开放回环业务入口，普通容器日志不持久敏感输出。
func composeService(plan servicedeploy.Plan, role string) map[string]any {
	policy := plan.Policy
	name, command, secret := plan.Resources.Application, "serve", "application_password"
	if role == "migration" {
		name, command, secret = plan.Resources.Migration, "migrate", "migration_password"
	}
	value := map[string]any{
		"image": plan.Candidate.Image.ID, "pull_policy": "never", "container_name": name, "entrypoint": []string{"/service"}, "command": []string{command}, "user": fmt.Sprintf("%d:%d", plan.Environment.UID, plan.Environment.GID),
		"environment": environment(plan, role), "secrets": []string{secret}, "networks": []string{"private"}, "restart": "no", "read_only": true, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "ipc": "private",
		"mem_limit": int64(policy.MemoryMiB) * 1024 * 1024, "memswap_limit": int64(policy.MemoryMiB) * 1024 * 1024, "cpus": policy.CPUs, "pids_limit": policy.PIDs, "tmpfs": []string{"/tmp:rw,noexec,nosuid,nodev,size=" + strconv.Itoa(policy.TmpfsMiB*1024*1024) + ",mode=1777"}, "logging": map[string]string{"driver": "none"}, "labels": labels(plan, role),
	}
	if role == "application" {
		value["networks"] = []string{"private", "ingress"}
		value["ports"] = []map[string]any{{"target": 8080, "published": strconv.Itoa(plan.Request.HostPort), "host_ip": "127.0.0.1", "protocol": "tcp"}}
	}
	return value
}

// composeMaterial 保留原MySQL基础文件，补充文件本身不引用管理秘密或数据卷。
func composeMaterial(plan servicedeploy.Plan, files mysqlruntime.BindingFiles) ([]byte, error) {
	if err := servicedeploy.ValidatePlan(plan); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"services": map[string]any{"application": composeService(plan, "application"), "migration": composeService(plan, "migration")}, "networks": map[string]any{"ingress": map[string]any{"external": true, "name": plan.Resources.IngressNetwork}}, "secrets": map[string]any{"application_password": map[string]string{"file": files.Application}, "migration_password": map[string]string{"file": files.Migration}}})
}
