// 本文件持有独立构建根与安装身份，不读取MySQL秘密或接管原运行目录。
package servicebuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"

	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// installation 是私有根唯一身份；丢失或损坏时不能重新初始化原根。
type installation struct {
	SchemaVersion int         `json:"schemaVersion"`
	ID            string      `json:"id"`
	Environment   Environment `json:"environment"`
}

// Driver 只拥有构建材料和本地Docker命令，不持有平台数据库或服务凭据。
type Driver struct {
	files        *privatefiles.Root
	installation installation
	execute      dockerCommand
}

// InitializeDirectory 只建立全新私有根；forbidden须包含所有仓库、静态目录和运行根。
func InitializeDirectory(directory string, forbidden []string, environment Environment) (err error) {
	if !validEnvironment(environment) {
		return failure("BUILD_ENVIRONMENT_INVALID", "构建初始化环境不满足固定本机组合")
	}
	files, err := privatefiles.Create(directory, forbidden)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, files.Close()) }()
	id, err := project.NewID()
	if err != nil {
		return err
	}
	data, err := json.Marshal(installation{1, id, environment})
	if err != nil {
		return err
	}
	if err := files.WriteNew("installation.json", data); err != nil {
		return err
	}
	for _, name := range []string{"operations", "locks"} {
		if err := files.Mkdir(name); err != nil {
			return err
		}
	}
	return nil
}

// OpenDriver 核实既有身份和完整私有边界，永不修复或覆盖原材料。
func OpenDriver(directory string, forbidden []string) (driver *Driver, err error) {
	files, err := privatefiles.Open(directory, forbidden)
	if err != nil {
		return nil, err
	}
	driver = &Driver{files: files, execute: dockerRun}
	defer func() {
		if err != nil {
			err = errors.Join(err, files.Close())
		}
	}()
	data, err := files.Read("installation.json", 16384)
	if err != nil {
		return nil, err
	}
	if decode(data, &driver.installation) != nil || driver.installation.SchemaVersion != 1 || !project.ValidID(driver.installation.ID) || !validEnvironment(driver.installation.Environment) {
		return nil, failure("BUILD_INSTALLATION_INVALID", "原构建安装材料不完整，保留现场并恢复原根")
	}
	for _, name := range []string{"operations", "locks"} {
		if err := files.CheckParents(name + "/entry"); err != nil {
			return nil, err
		}
	}
	return driver, nil
}

// InstallationID 返回固定非秘密身份，不能由HTTP请求指定。
func (driver *Driver) InstallationID() string { return driver.installation.ID }

// Environment 返回冻结副本，后续执行仍须核实实际工具与原环境相同。
func (driver *Driver) Environment() Environment { return driver.installation.Environment }

// Close 仅释放目录句柄，不删除材料或停止未知资源。
func (driver *Driver) Close() error { return driver.files.Close() }

// Preflight 拒绝以升级工具、镜像或daemon静默复用旧检查证据。
func (driver *Driver) Preflight(ctx context.Context) error {
	actual, err := discover(ctx, driver.execute, driver.Environment().Context)
	if err != nil {
		return err
	}
	if actual != driver.Environment() {
		return failure("BUILD_ENVIRONMENT_CHANGED", "构建工具环境已改变，须显式核实原安装与新策略")
	}
	return nil
}

// validEnvironment 在私有清单边界应用同一支持约束，不猜测缺失值。
func validEnvironment(value Environment) bool {
	mode := value.BindSourceMode == "canonical" || runtime.GOOS == "darwin" && value.BindSourceMode == "desktop-host-mnt-v1"
	return mode && safeName.MatchString(value.Context) && value.BuilderName == value.Context && safeDaemon.MatchString(value.DaemonID) && versionPattern.MatchString(value.DockerVersion) && value.BuildxVersion != "" && versionPattern.MatchString(value.BuildkitVersion) && digestPattern.MatchString(value.ImageID) && value.ImageReference == ToolchainImage && hasExact([]string{"amd64", "arm64"}, value.Architecture) && value.OperatingSystem == "linux" && value.UID > 0 && value.GID >= 0 && digestPattern.MatchString(value.EndpointDigest)
}

// decode 限制私有机器材料，拒绝未知字段或尾随对象。
func decode(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return failure("BUILD_MATERIAL_INVALID", "构建材料包含额外内容")
	}
	return nil
}
