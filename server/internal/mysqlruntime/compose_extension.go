// 本文件把已接受的业务overlay加入原Compose项目，既不忽略孤儿告警也不改写MySQL基础材料。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// ComposeOverlay 只能定位原资源中一个服务的固定材料，不携带任意宿主路径。
type ComposeOverlay struct {
	ServiceID string
	Data      []byte
	// Unmaterialized 仅由受信提供方在历史材料未完成且实际业务资源全部缺席后授予。
	Unmaterialized bool
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
			name, err := driver.composeExtensionFile(ctx, resource, *overlay)
			if err != nil {
				return nil, err
			}
			if name != "" {
				args = append(args, "--file", name)
			}
		}
	}
	return driver.execute(ctx, append(args, action...)...)
}

// composeExtensionFile 验证既有字节；只有尚未完成且无业务资源的扩展可以不加入MySQL命令。
func (driver *Driver) composeExtensionFile(ctx context.Context, resource Resource, overlay ComposeOverlay) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !project.ValidID(overlay.ServiceID) || len(overlay.Data) == 0 || len(overlay.Data) > 32768 {
		return "", failure("DEPLOY_MATERIAL_INVALID", "业务扩展不属于已接受的受控材料", false, false)
	}
	prefix := projectFilePrefix + resource.ID + "/services/" + overlay.ServiceID
	if overlay.Unmaterialized {
		present, err := driver.unmaterializedParents(ctx, resource.ID, overlay.ServiceID)
		if err != nil || !present {
			return "", err
		}
	}
	data, err := driver.readPrivate(prefix+"/compose.json", 32768)
	if overlay.Unmaterialized && errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil || !bytes.Equal(data, overlay.Data) {
		return "", failure("DEPLOY_MATERIAL_CONFLICT", "原业务Compose材料缺失或被改写，恢复前保留原资源", false, false)
	}
	if overlay.Unmaterialized {
		return "", nil
	}
	return filepath.Join(driver.directory, prefix, "compose.json"), nil
}

// unmaterializedParents 逐段检查安全父路径后才接受缺席，不把权限错误、链接或特殊文件当未生成。
func (driver *Driver) unmaterializedParents(ctx context.Context, resourceID, serviceID string) (bool, error) {
	for _, name := range []string{projectFilePrefix + resourceID + "/services", projectFilePrefix + resourceID + "/services/" + serviceID} {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := driver.privateParents(name); err != nil {
			return false, err
		}
		if _, err := os.Lstat(filepath.Join(driver.directory, name)); errors.Is(err, fs.ErrNotExist) {
			return false, nil
		} else if err != nil {
			return false, failure("DEPLOY_MATERIAL_CONFLICT", "无法核实业务材料目录，保留原对象", false, true)
		}
	}
	return true, nil
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
