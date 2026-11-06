// 本文件把已接受的业务overlay加入原Compose项目，既不忽略孤儿告警也不改写MySQL基础材料。
package mysqlruntime

import (
	"bytes"
	"context"
	"path/filepath"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// ComposeOverlay 只能定位原资源中一个服务的固定材料，不携带任意宿主路径。
type ComposeOverlay struct {
	ServiceID string
	Data      []byte
}

// ComposeExtensions 由持久部署模块提供当前已接受目标的确定材料。
type ComposeExtensions interface {
	RuntimeOverlay(context.Context, Resource) (*ComposeOverlay, error)
}

// SetComposeExtensions 仅供启动装配一次，必须先于任何工作器启动。
func (driver *Driver) SetComposeExtensions(provider ComposeExtensions) error {
	if provider == nil || driver.extensions != nil {
		return failure("DEPLOY_CONFIGURATION_INVALID", "Compose扩展只能显式装配一次", false, false)
	}
	driver.extensions = provider
	return nil
}

// composeCommand 每次从持久目标重建期望字节，并核实私有文件；缺失时不偷偷生成业务材料。
func (driver *Driver) composeCommand(ctx context.Context, resource Resource, action ...string) ([]byte, error) {
	args := driver.composeArguments(resource)
	if driver.extensions != nil {
		overlay, err := driver.extensions.RuntimeOverlay(ctx, resource)
		if err != nil {
			return nil, err
		}
		if overlay != nil {
			if !project.ValidID(overlay.ServiceID) || len(overlay.Data) == 0 || len(overlay.Data) > 32768 {
				return nil, failure("DEPLOY_MATERIAL_INVALID", "业务扩展不属于已接受的受控材料", false, false)
			}
			name := "projects/" + resource.ID + "/services/" + overlay.ServiceID + "/compose.json"
			data, err := driver.readPrivate(name, 32768)
			if err != nil || !bytes.Equal(data, overlay.Data) {
				return nil, failure("DEPLOY_MATERIAL_CONFLICT", "原业务Compose材料缺失或被改写，恢复前保留原资源", false, false)
			}
			args = append(args, "--file", filepath.Join(driver.directory, name))
		}
	}
	return driver.execute(ctx, append(args, action...)...)
}

// Start 只启动固定角色，不接受任意Compose动作、镜像或服务名称。
func (session *BindingSession) Start(ctx context.Context, role string) error {
	if role != "application" && role != "migration" {
		return failure("DEPLOY_PLAN_INVALID", "业务容器角色无效", false, false)
	}
	if err := session.driver.prepareCompose(ctx, session.request.Resource); err != nil {
		return err
	}
	_, err := session.driver.composeCommand(ctx, session.request.Resource, "up", "--detach", "--no-build", "--no-deps", "--no-recreate", "--pull", "never", role)
	return err
}
