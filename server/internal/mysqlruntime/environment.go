// 本文件只读核实本地 Unix socket daemon、Compose 和固定镜像，不自动安装或拉取。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// safeContext 限定 Docker context 名称，后续调用始终显式固定同一 context。
var safeContext = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// safeDaemonIdentity 接受Docker的UUID及旧式分段ID，不接受控制字符或任意参数文本。
var safeDaemonIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// imageIdentity 只接受完整内容摘要，不能用部分前缀冒充固定镜像身份。
var imageIdentity = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// imageSpec 保留严格核实容器时所需的只读镜像默认值。
type imageSpec struct {
	ID          string
	RepoDigests []string
	Config      struct {
		Env        []string
		Entrypoint []string
		User       string
	}
}

// DiscoverEnvironment 核实工具与缓存，不改变 Docker 中任何资源。
func DiscoverEnvironment(ctx context.Context) (Environment, error) {
	value, _, err := discover(ctx, localDocker, "")
	return value, err
}

// discover 拒绝远程 context、非 Linux daemon 与没有固定 digest 的镜像。
func discover(ctx context.Context, execute dockerCommand, contextName string) (Environment, imageSpec, error) {
	var value Environment
	var image imageSpec
	name, err := localContext(ctx, execute, contextName)
	if err != nil {
		return value, image, err
	}
	value.Context = name
	if err := dockerJSON(ctx, execute, value, "info", &structInfo{target: &value}); err != nil {
		return value, image, err
	}
	data, err := execute(ctx, contextOption, name, "compose", "version", "--short")
	if err != nil {
		return value, image, err
	}
	value.ComposeVersion = strings.TrimSpace(string(data))
	if !supportedCompose(value.ComposeVersion) {
		return value, image, failure("COMPOSE_UNSUPPORTED", "需要支持本切片配置的 Compose 插件2.24或以上版本", false, false)
	}
	image, err = pinnedImage(ctx, execute, name)
	value.ImageID = image.ID
	return value, image, err
}

// localContext 将本地Unix socket核实与镜像发现分开，远程环境在任何运行计划前拒绝。
func localContext(ctx context.Context, execute dockerCommand, contextName string) (string, error) {
	if contextName == "" {
		data, err := execute(ctx, "context", "show")
		if err != nil {
			return "", err
		}
		contextName = strings.TrimSpace(string(data))
	}
	if !safeContext.MatchString(contextName) {
		return "", failure("DOCKER_CONTEXT_UNSUPPORTED", "Docker context 名称不受本机方案支持", false, false)
	}
	data, err := execute(ctx, "context", "inspect", contextName)
	if err != nil {
		return "", err
	}
	var contexts []struct {
		Endpoints map[string]struct{ Host string }
	}
	if json.Unmarshal(data, &contexts) != nil || len(contexts) != 1 {
		return "", failure("DOCKER_OUTPUT_INVALID", "Docker context 结果无效", false, false)
	}
	endpoint, err := url.Parse(contexts[0].Endpoints["docker"].Host)
	if err != nil || endpoint.Scheme != "unix" || endpoint.Host != "" || !strings.HasPrefix(endpoint.Path, "/") {
		return "", failure("DOCKER_CONTEXT_UNSUPPORTED", "仅支持明确的本地 Unix socket Docker context", false, false)
	}
	return contextName, nil
}

// pinnedImage 同时核实原仓库digest与完整镜像内容身份，不因工具失败替换镜像。
func pinnedImage(ctx context.Context, execute dockerCommand, contextName string) (imageSpec, error) {
	var image imageSpec
	data, err := execute(ctx, contextOption, contextName, "image", "inspect", Image)
	if err != nil {
		return image, failure("MYSQL_IMAGE_UNAVAILABLE", "本地缺少目录固定镜像，请准备镜像后再核实", false, false)
	}
	var images []imageSpec
	if json.Unmarshal(data, &images) != nil || len(images) != 1 {
		return image, failure("DOCKER_OUTPUT_INVALID", "固定镜像的元数据无效", false, false)
	}
	image = images[0]
	repositoryDigest := "mysql@" + strings.SplitN(Image, "@", 2)[1]
	if !imageIdentity.MatchString(image.ID) || !contains(image.RepoDigests, repositoryDigest) {
		return image, failure("MYSQL_IMAGE_CONFLICT", "镜像内容身份与目录不一致", false, false)
	}
	return image, nil
}

// structInfo 仅从本机守护进程接收所需身份，忽略无关系统信息。
type structInfo struct{ target *Environment }

// UnmarshalJSON 在外部边界核实 daemon 类型与非空稳定身份。
func (info *structInfo) UnmarshalJSON(data []byte) error {
	var value struct{ ID, OSType, OperatingSystem string }
	if json.Unmarshal(data, &value) != nil || value.OSType != "linux" || !safeDaemonIdentity.MatchString(value.ID) {
		return failure("DOCKER_DAEMON_UNSUPPORTED", "需要具有稳定身份的 Linux Docker daemon", false, false)
	}
	info.target.DaemonID = value.ID
	info.target.BindSourceMode = "native"
	if runtime.GOOS == "darwin" && value.OperatingSystem == "Docker Desktop" {
		info.target.BindSourceMode = "desktop-host-mnt-v1"
	}
	return nil
}

// validEnvironment 核实私有清单也使用发现时的边界，不把损坏身份带入后续执行。
func validEnvironment(value Environment) bool {
	modeValid := value.BindSourceMode == "native" || (runtime.GOOS == "darwin" && value.BindSourceMode == "desktop-host-mnt-v1")
	return safeContext.MatchString(value.Context) && safeDaemonIdentity.MatchString(value.DaemonID) && imageIdentity.MatchString(value.ImageID) && supportedCompose(value.ComposeVersion) && modeValid
}

// dockerJSON 统一使用固定 context 和 JSON 输出，错误保持安全且有界。
func dockerJSON(ctx context.Context, execute dockerCommand, environment Environment, command string, target any) error {
	data, err := execute(ctx, contextOption, environment.Context, command, "--format", "{{json .}}")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return failure("DOCKER_OUTPUT_INVALID", "Docker 返回不符合受控读取契约", false, false)
	}
	return nil
}

// supportedCompose 只依赖已存在的 Compose 基础能力，具体验证版本仍随诊断记录。
func supportedCompose(value string) bool {
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > 2 || major == 2 && minor >= 24
}

// contains 对已校验的小集合精确比较，不按标签或部分摘要猜测身份。
func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
