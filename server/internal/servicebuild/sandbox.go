// 本文件建立固定受限沙箱；依赖准备与任意业务代码执行之间必须断网并核实。
package servicebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sandbox 固定单次构建身份，不使用用户显示名或可变tag命名。
type sandbox struct {
	name, directory string
	target          Target
	operationID     string
}

// newSandbox 的外部名字绑定安装、项目与操作，不与运行服务共享资源。
func (driver *Driver) newSandbox(target Target, operationID string) sandbox {
	hash := sha256.Sum256([]byte(driver.InstallationID() + "/" + target.ProjectID + "/" + operationID))
	return sandbox{"tr-build-" + hex.EncodeToString(hash[:16]), filepath.Join(driver.files.Path(), "operations", operationID), target, operationID}
}

// labels 是外部归属的必要证据，不能单靠名字前缀授权清理。
func (box sandbox) labels() map[string]string {
	return map[string]string{"io.twine-realm.build.installation": box.target.InstallationID, "io.twine-realm.build.operation": box.operationID, "io.twine-realm.build.target": box.target.ID, "io.twine-realm.build.input": box.target.InputDigest}
}

// sandboxEnvironment 不继承宿主数据库、HOME、代理密码或Go环境覆盖。
func sandboxEnvironment() map[string]string {
	return map[string]string{"HOME": "/tmp/home", "GOCACHE": "/tmp/gocache", "GOMODCACHE": "/tmp/gomod", "GOPATH": "/tmp/go", "GOTMPDIR": "/tmp/gotmp", "GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly -p=2", "GOMAXPROCS": "2", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", "GOPRIVATE": "", "GONOPROXY": "", "GONOSUMDB": "", "GOVCS": "*:off", "CGO_ENABLED": "1"}
}

// createArgs 将限制全部作为明确参数，不执行服务提供的Dockerfile或启动脚本。
func (box sandbox) createArgs() []string {
	policy, environment := box.target.Policy, box.target.Environment
	memory := strconv.Itoa(policy.MemoryMiB * 1024 * 1024)
	args := []string{"container", "create", "--pull=never", "--name", box.name, "--user", fmt.Sprintf("%d:%d", environment.UID, environment.GID), "--workdir", sourceMountPath, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--pids-limit", strconv.Itoa(policy.Pids), "--memory", memory, "--memory-swap", memory, "--cpus", strconv.Itoa(policy.CPUs), "--network", "bridge", "--ipc", "private", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=" + strconv.Itoa(policy.TmpfsMiB*1024*1024), "--mount", "type=bind,source=" + filepath.Join(box.directory, "source") + ",target=/source,readonly", "--entrypoint", "/usr/bin/tail"}
	args = append(args, "--mount", "type=volume,source="+box.name+"-out,target=/output,volume-nocopy")
	for _, key := range sortedKeys(box.labels()) {
		args = append(args, "--label", key+"="+box.labels()[key])
	}
	for _, key := range sortedKeys(sandboxEnvironment()) {
		args = append(args, "--env", key+"="+sandboxEnvironment()[key])
	}
	return append(args, environment.ImageID, "-f", "/dev/null")
}

// sortedKeys 保持外部计划顺序稳定，完整匹配仍按键值而非JSON字段顺序。
func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// run 将所有外部动作固定到原context并保持每条命令的限额。
func (driver *Driver) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := driver.verifyBoundary(ctx); err != nil {
		return nil, err
	}
	return driver.execute(ctx, time.Duration(DefaultPolicy().CommandSeconds)*time.Second, false, append([]string{dockerContextOption, driver.Environment().Context}, args...)...)
}

// createSandbox 拒绝任何既有同名对象；未知create结果留待原操作核实。
func (driver *Driver) createSandbox(ctx context.Context, box sandbox) (string, error) {
	value, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return "", err
	}
	if value != nil {
		return "", ownershipConflict()
	}
	if err := driver.createOutputVolume(ctx, box); err != nil {
		return "", err
	}
	data, err := driver.run(ctx, box.createArgs()...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !containerIDPattern.MatchString(id) {
		return "", invalidOutput()
	}
	value, err = driver.inspectSandbox(ctx, box)
	if err != nil {
		return "", err
	}
	if value == nil || value.ID != id {
		return "", ownershipConflict()
	}
	if _, err := driver.run(ctx, "container", "start", id); err != nil {
		return "", err
	}
	value, err = driver.inspectSandbox(ctx, box)
	if err != nil {
		return "", err
	}
	if value == nil || value.ID != id || !value.State.Running {
		return "", failure("BUILD_SANDBOX_NOT_RUNNING", "隔离构建容器未确认运行")
	}
	return id, nil
}

// offline 在执行业务代码之前断开唯一网络；不能把命令成功代替实际核实。
func (driver *Driver) offline(ctx context.Context, box sandbox, id string) error {
	value, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return err
	}
	if value == nil || value.ID != id || !value.State.Running {
		return ownershipConflict()
	}
	if _, err := driver.run(ctx, "network", "disconnect", "bridge", id); err != nil {
		return err
	}
	value, err = driver.inspectSandbox(ctx, box)
	if err != nil {
		return err
	}
	if value == nil || value.ID != id || len(value.NetworkSettings.Networks) != 0 {
		return failure("BUILD_NETWORK_UNCONFIRMED", "业务代码检查前未确认沙箱断网")
	}
	return nil
}

// cleanupSandbox 只停止并移除完整核实的本次容器；外来或改名对象保持原样。
func (driver *Driver) cleanupSandbox(ctx context.Context, box sandbox) (err error) {
	defer func() {
		if err == nil {
			err = driver.cleanupOutputVolume(ctx, box)
		}
	}()
	value, err := driver.inspectSandbox(ctx, box)
	if err != nil || value == nil {
		return err
	}
	if value.State.Paused {
		if _, err := driver.run(ctx, "container", "unpause", value.ID); err != nil {
			return err
		}
	}
	if value.State.Running {
		if _, err := driver.run(ctx, "container", "stop", "--time", "3", value.ID); err != nil {
			return err
		}
	}
	current, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return err
	}
	if current == nil || current.ID != value.ID || current.State.Running {
		return ownershipConflict()
	}
	_, err = driver.run(ctx, "container", "rm", current.ID)
	return err
}
