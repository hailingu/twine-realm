// 本文件只生成目录固定的 Compose 材料，所有网络和卷必须先按归属登记与核实。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// labels 明确安装、项目、组件与资源身份，不以名称前缀接管外部对象。
func labels(resource Resource) map[string]string {
	return map[string]string{
		"io.twine-realm.adapter":       "mysql-compose-v1",
		"io.twine-realm.installation":  resource.InstallationID,
		"io.twine-realm.project":       resource.ProjectID,
		"io.twine-realm.component":     resource.ComponentID,
		"io.twine-realm.resource":      resource.ID,
		"io.twine-realm.configuration": resource.ConfigurationHash,
	}
}

// validateResource 对持久计划重新核实目录、摘要、身份和确定名称，拒绝损坏载荷执行。
func (driver *Driver) validateResource(resource Resource) error {
	if !project.ValidID(resource.ID) || !project.ValidID(resource.ProjectID) || !project.ValidID(resource.ComponentID) || resource.InstallationID != driver.InstallationID() || resource.DaemonID != driver.Environment().DaemonID || resource.Image != Image {
		return failure("RUNTIME_OWNERSHIP_CONFLICT", "运行计划不属于此安装或固定环境", false, false)
	}
	expected := resource.WithID(resource.ID)
	if resource.Network != expected.Network || resource.Volume != expected.Volume || resource.Container != expected.Container || resource.SecretRef != expected.SecretRef {
		return failure("RUNTIME_PLAN_INVALID", "资源名称与固定身份不一致", false, false)
	}
	definitions, err := catalog.New()
	if err != nil {
		return failure("RUNTIME_PLAN_INVALID", "固定目录不可用", false, false)
	}
	configuration, reasons, err := definitions.Normalize(resource.Definition, resource.Configuration)
	if err != nil || len(reasons) != 0 || configuration["runtimeProfile"] != "local-compose-v1" {
		return failure("RUNTIME_PLAN_INVALID", "持久配置不属于完整受支持组合", false, false)
	}
	hash, err := Fingerprint(struct {
		Definition    project.DefinitionRef
		Configuration map[string]any
	}{resource.Definition, configuration})
	if err != nil || hash != resource.ConfigurationHash {
		return failure("RUNTIME_PLAN_INVALID", "持久配置摘要不一致", false, false)
	}
	return nil
}

// preflight 每次执行或采集都核实相同 daemon 与镜像，不因 context 切换接管另一实例。
func (driver *Driver) preflight(ctx context.Context, resource Resource) (imageSpec, error) {
	if err := driver.validateResource(resource); err != nil {
		return imageSpec{}, err
	}
	if err := driver.privateParents("installation.json"); err != nil {
		return imageSpec{}, err
	}
	environment, image, err := discover(ctx, driver.execute, driver.Environment().Context)
	if err != nil {
		return imageSpec{}, err
	}
	if environment.DaemonID != driver.Environment().DaemonID || environment.ImageID != driver.Environment().ImageID || environment.BindSourceMode != driver.Environment().BindSourceMode {
		return imageSpec{}, failure("DOCKER_IDENTITY_CONFLICT", "Docker daemon或镜像身份已改变，原资源保留", false, false)
	}
	return image, nil
}

// composeMaterial 引用固定受限文件，不写密码环境值、宿主端口或用户命令。
func (driver *Driver) composeMaterial(resource Resource) ([]byte, error) {
	memory, ok := resource.Configuration["memoryMiB"].(float64)
	if !ok || memory < 256 || memory > 8192 {
		return nil, failure("RUNTIME_PLAN_INVALID", "目录内存配置无效", false, false)
	}
	prefix := filepath.Join(driver.directory, "projects", resource.ID)
	service := map[string]any{
		"image": Image, "pull_policy": "never", "container_name": resource.Container,
		"command":     []string{"mysqld", "--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci"},
		"environment": map[string]string{"MYSQL_ROOT_PASSWORD_FILE": "/run/secrets/root_password"},
		"secrets":     []string{"root_password", "client_options"},
		"volumes":     []any{map[string]any{"type": "volume", "source": "data", "target": "/var/lib/mysql", "volume": map[string]bool{"nocopy": true}}},
		"networks":    []string{"private"}, "restart": "unless-stopped", "mem_limit": int64(memory) * 1024 * 1024,
		"security_opt": []string{"no-new-privileges:true"}, "labels": labels(resource),
		"healthcheck": map[string]any{"test": []string{"CMD", "mysql", "--defaults-extra-file=/run/secrets/client_options", "--batch", "--skip-column-names", "--execute=SELECT 1"}, "interval": "5s", "timeout": "3s", "retries": 3, "start_period": "5s"},
	}
	value := map[string]any{
		"services": map[string]any{"mysql": service},
		"networks": map[string]any{"private": map[string]any{"external": true, "name": resource.Network}},
		"volumes":  map[string]any{"data": map[string]any{"external": true, "name": resource.Volume}},
		"secrets":  map[string]any{"root_password": map[string]string{"file": filepath.Join(prefix, "root.secret")}, "client_options": map[string]string{"file": filepath.Join(prefix, "client.secret")}},
	}
	return json.Marshal(value)
}

// prepareCompose 对固定材料逐字节核实，避免外部编辑成为未接受的新配置。
func (driver *Driver) prepareCompose(ctx context.Context, resource Resource) error {
	data, err := driver.composeMaterial(resource)
	if err != nil {
		return err
	}
	prefix := "projects/" + resource.ID + "/"
	if err := driver.ensureMaterial(prefix+"compose.json", data); err != nil {
		return err
	}
	if err := driver.ensureMaterial(prefix+"compose.env", []byte{}); err != nil {
		return err
	}
	_, err = driver.execute(ctx, driver.composeArguments(resource, "config", "--quiet")...)
	return err
}

// composeArguments 固定项目、文件及空插值环境，不读取用户仓库的 Compose 或 .env。
func (driver *Driver) composeArguments(resource Resource, action ...string) []string {
	prefix := filepath.Join(driver.directory, "projects", resource.ID)
	args := []string{contextOption, driver.Environment().Context, "compose", "--project-name", resource.Container, "--project-directory", prefix, "--env-file", filepath.Join(prefix, "compose.env"), "--file", filepath.Join(prefix, "compose.json")}
	return append(args, action...)
}
