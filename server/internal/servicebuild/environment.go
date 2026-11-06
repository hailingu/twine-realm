// 本文件核实本机工具链的完整内容身份，不自动安装、拉取或选择远程builder。
package servicebuild

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
)

// dockerContextOption 强制每条Docker命令沿用原context。
const dockerContextOption = "--context"

// sourceMountPath 是唯一只读服务源码挂载点。
const sourceMountPath = "/source"

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var safeDaemon = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9_.-]+)?$`)

// dockerCommand 是本适配器的有界进程端口；不接受用户shell或环境变量。
type dockerCommand func(context.Context, time.Duration, bool, ...string) ([]byte, error)

// dockerRun 将诊断策略固定在共享进程边界，原始输出只由可信解析器消费。
func dockerRun(ctx context.Context, timeout time.Duration, metadata bool, args ...string) ([]byte, error) {
	return localdocker.Run(ctx, localdocker.Policy{Timeout: timeout, BuildMetadata: metadata}, args...)
}

// DiscoverEnvironment 只读核实当前本地context、缓存镜像与docker-driver builder。
func DiscoverEnvironment(ctx context.Context) (Environment, error) {
	return discover(ctx, dockerRun, "")
}

// discover 冻结原context而不依赖后续用户全局选择，非root宿主用户是首版边界。
func discover(ctx context.Context, execute dockerCommand, name string) (value Environment, err error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" || os.Getuid() <= 0 {
		return value, failure("BUILD_HOST_UNSUPPORTED", "隔离构建需要Mac/Linux上的非root可信用户")
	}
	name, endpoint, err := localContext(ctx, execute, name)
	if err != nil {
		return value, err
	}
	value.Context, value.BuilderName = name, name
	value.EndpointDigest, err = Fingerprint(endpoint)
	if err != nil {
		return value, err
	}
	value.UID, value.GID = os.Getuid(), os.Getgid()
	if err := inspectDaemon(ctx, execute, &value); err != nil {
		return value, err
	}
	if err := inspectBuilder(ctx, execute, &value); err != nil {
		return value, err
	}
	if err := inspectToolchain(ctx, execute, &value); err != nil {
		return value, err
	}
	return value, nil
}

// localContext 只接受无远程主机、查询或TLS覆盖的明确Unix socket。
func localContext(ctx context.Context, execute dockerCommand, name string) (string, string, error) {
	if name == "" {
		data, err := execute(ctx, 15*time.Second, false, "context", "show")
		if err != nil {
			return "", "", err
		}
		name = strings.TrimSpace(string(data))
	}
	if !safeName.MatchString(name) {
		return "", "", failure("BUILD_CONTEXT_UNSUPPORTED", "Docker context名称不受支持")
	}
	data, err := execute(ctx, 15*time.Second, false, "context", "inspect", name)
	if err != nil {
		return "", "", err
	}
	var values []struct {
		Endpoints map[string]struct {
			Host          string
			SkipTLSVerify bool
		}
	}
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return "", "", invalidOutput()
	}
	endpoint := values[0].Endpoints["docker"]
	uri, err := url.Parse(endpoint.Host)
	if err != nil || uri.Scheme != "unix" || uri.Host != "" || !strings.HasPrefix(uri.Path, "/") || uri.RawQuery != "" || uri.Fragment != "" || endpoint.SkipTLSVerify {
		return "", "", failure("BUILD_CONTEXT_UNSUPPORTED", "仅支持明确的本地Unix socket Docker context")
	}
	return name, uri.String(), nil
}

// inspectDaemon 排除Windows与非明确架构，Docker Desktop路径转换属于冻结环境。
func inspectDaemon(ctx context.Context, execute dockerCommand, value *Environment) error {
	data, err := execute(ctx, 15*time.Second, false, dockerContextOption, value.Context, "info", "--format", "{{json .}}")
	if err != nil {
		return err
	}
	var info struct{ ID, OSType, OperatingSystem, ServerVersion, Architecture string }
	if json.Unmarshal(data, &info) != nil || info.OSType != "linux" || !safeDaemon.MatchString(info.ID) || !versionPattern.MatchString(info.ServerVersion) {
		return invalidOutput()
	}
	architecture := map[string]string{"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[info.Architecture]
	if architecture == "" {
		return failure("BUILD_ARCHITECTURE_UNSUPPORTED", "工具链仅验证linux/amd64与linux/arm64")
	}
	value.DaemonID, value.DockerVersion, value.Architecture, value.OperatingSystem = info.ID, info.ServerVersion, architecture, "linux"
	value.BindSourceMode = "canonical"
	if runtime.GOOS == "darwin" && info.OperatingSystem == "Docker Desktop" {
		value.BindSourceMode = "desktop-host-mnt-v1"
	}
	return nil
}

// inspectBuilder 显式选取同context的docker-driver，禁止隐式远程builder与自动bootstrap。
func inspectBuilder(ctx context.Context, execute dockerCommand, value *Environment) error {
	data, err := execute(ctx, 15*time.Second, false, dockerContextOption, value.Context, "buildx", "version")
	if err != nil {
		return err
	}
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) != 3 || parts[0] != "github.com/docker/buildx" || !versionPattern.MatchString(parts[1]) {
		return invalidOutput()
	}
	value.BuildxVersion = parts[1] + " " + parts[2]
	data, err = execute(ctx, 15*time.Second, false, dockerContextOption, value.Context, "buildx", "inspect", value.BuilderName)
	if err != nil {
		return err
	}
	version, err := localBuilderVersion(string(data), value.Context)
	value.BuildkitVersion = version
	return err
}

// localBuilderVersion 只解析固定摘要区；节点数与端点均须明确一致。
func localBuilderVersion(text, name string) (string, error) {
	fields := map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		if key == "Labels" || key == "Devices" || key == "GC Policy rule#0" {
			break
		}
		fields[key] = append(fields[key], strings.TrimSpace(value))
	}
	if strings.Join(fields["Name"], ",") != name+","+name || strings.Join(fields["Driver"], ",") != "docker" || strings.Join(fields["Endpoint"], ",") != name || strings.Join(fields["Status"], ",") != "running" || len(fields["BuildKit version"]) != 1 {
		return "", failure("BUILD_BUILDER_UNSUPPORTED", "需要同一本地context的单节点docker-driver builder")
	}
	version := fields["BuildKit version"][0]
	if !versionPattern.MatchString(version) {
		return "", invalidOutput()
	}
	return version, nil
}

// inspectToolchain 同时验证仓库digest、实际架构和完整镜像ID，不把tag当作证据。
func inspectToolchain(ctx context.Context, execute dockerCommand, value *Environment) error {
	data, err := execute(ctx, 15*time.Second, false, dockerContextOption, value.Context, "image", "inspect", ToolchainImage)
	if err != nil {
		return failure("BUILD_IMAGE_UNAVAILABLE", "本地缺少固定Go工具链镜像，请准备后重新核实")
	}
	var images []struct {
		ID, Architecture, Os string
		RepoDigests          []string
		Config               struct{ Env []string }
	}
	if json.Unmarshal(data, &images) != nil || len(images) != 1 {
		return invalidOutput()
	}
	image := images[0]
	if !digestPattern.MatchString(image.ID) || image.Architecture != value.Architecture || image.Os != "linux" || !hasExact(image.RepoDigests, "golang@"+strings.SplitN(ToolchainImage, "@", 2)[1]) || !hasExact(image.Config.Env, "GOLANG_VERSION=1.27.3") {
		return failure("BUILD_IMAGE_CONFLICT", "工具链镜像身份或架构与已验证组合不一致")
	}
	value.ImageID, value.ImageReference = image.ID, ToolchainImage
	return nil
}

// hasExact 不接受前缀或模糊标签匹配。
func hasExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// invalidOutput 不把外部不完整结果当作默认可执行环境。
func invalidOutput() error {
	return failure("BUILD_OUTPUT_INVALID", "构建工具返回不符合受控读取契约")
}

// verifyBoundary 每个资源动作核实原Unix端点与daemon；清理不要求原工具镜像仍缓存。
func (driver *Driver) verifyBoundary(ctx context.Context) error {
	environment := driver.Environment()
	_, endpoint, err := localContext(ctx, driver.execute, environment.Context)
	if err != nil {
		return err
	}
	digest, err := Fingerprint(endpoint)
	if err != nil {
		return err
	}
	if digest != environment.EndpointDigest {
		return failure("BUILD_ENVIRONMENT_CHANGED", "原Docker端点已改变，保留资源等待核实")
	}
	data, err := driver.execute(ctx, 15*time.Second, false, dockerContextOption, environment.Context, "info", "--format", "{{json .ID}}")
	if err != nil {
		return err
	}
	var identity string
	if json.Unmarshal(data, &identity) != nil || identity != environment.DaemonID {
		return failure("BUILD_ENVIRONMENT_CHANGED", "原Docker daemon身份已改变，保留资源等待核实")
	}
	return nil
}
