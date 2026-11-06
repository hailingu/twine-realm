// Package servicebuild 将固定服务源码变为经过检查的本地不可变镜像候选，不接受业务部署。
package servicebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// ToolchainImage 固定首个受支持工具链，不使用可变tag替代内容身份。
const ToolchainImage = "golang:1.27.3@sha256:0c050feb47802ae436b9030833694a0b80160d08e3b3049ad30bf7c9e724e09d"

// Policy 固定执行限额；升级这些语义必须发布新的策略身份。
type Policy struct {
	ID             string `json:"id"`
	Version        int    `json:"version"`
	TotalSeconds   int    `json:"totalSeconds"`
	CommandSeconds int    `json:"commandSeconds"`
	SourceBytes    int64  `json:"sourceBytes"`
	SourceFiles    int    `json:"sourceFiles"`
	BinaryBytes    int64  `json:"binaryBytes"`
	MemoryMiB      int    `json:"memoryMiB"`
	TmpfsMiB       int    `json:"tmpfsMiB"`
	Pids           int    `json:"pids"`
	CPUs           int    `json:"cpus"`
}

// DefaultPolicy 是待真实验证的第一组合，不能由HTTP请求放宽。
func DefaultPolicy() Policy {
	return Policy{"go-container-local-v1", 1, 480, 180, project.SourceExportBytes, project.SourceExportFiles, 128 * 1024 * 1024, 4096, 3072, 256, 2}
}

// Environment 冻结本地daemon、builder、工具链内容与运行用户，不含密码或socket位置。
type Environment struct {
	Context         string `json:"context"`
	DaemonID        string `json:"daemonId"`
	DockerVersion   string `json:"dockerVersion"`
	BuildxVersion   string `json:"buildxVersion"`
	BuilderName     string `json:"builderName"`
	BuildkitVersion string `json:"buildkitVersion"`
	ImageID         string `json:"imageId"`
	ImageReference  string `json:"imageReference"`
	Architecture    string `json:"architecture"`
	OperatingSystem string `json:"operatingSystem"`
	BindSourceMode  string `json:"bindSourceMode"`
	UID             int    `json:"uid"`
	GID             int    `json:"gid"`
	EndpointDigest  string `json:"endpointDigest"`
}

// Source 是已固定且非空的仓库关联；画布revision仅用于受理并发，不作为构建内容。
type Source struct {
	ProjectID       string `json:"projectId"`
	ProjectRevision string `json:"projectRevision"`
	ServiceID       string `json:"serviceId"`
	RepositoryID    string `json:"repositoryId"`
	RepositoryPath  string `json:"repositoryPath"`
	PinnedCommit    string `json:"pinnedCommit"`
}

// Target 是受理后不可变的完整构建输入和执行策略。
type Target struct {
	ID             string               `json:"id"`
	ProjectID      string               `json:"projectId"`
	ServiceID      string               `json:"serviceId"`
	Generation     string               `json:"generation"`
	CanvasRevision string               `json:"canvasRevision"`
	InstallationID string               `json:"installationId"`
	Source         Source               `json:"source"`
	Profile        engineering.Identity `json:"profile"`
	Environment    Environment          `json:"environment"`
	Policy         Policy               `json:"policy"`
	InputDigest    string               `json:"inputDigest"`
	CreatedAt      string               `json:"createdAt"`
}

// Command 明确构建用户已固定的commit，不接受任意命令、Dockerfile或运行秘密。
type Command struct {
	MutationID     string               `json:"mutationId"`
	BaseRevision   string               `json:"baseRevision"`
	BaseGeneration string               `json:"baseGeneration"`
	Commit         string               `json:"commit"`
	Profile        engineering.Identity `json:"profile"`
	RetryOf        string               `json:"retryOf,omitempty"`
}

// CancelCommand 独立持久接受原操作取消；BaseVersion为已观察版本下界，不受进度前进竞争影响。
type CancelCommand struct {
	MutationID  string `json:"mutationId"`
	BaseVersion string `json:"baseVersion"`
}

// Step 是可查询检查点，executing不等于外部检查已经完成。
type Step struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Code      string `json:"code"`
	UpdatedAt string `json:"updatedAt"`
}

// Operation 拥有进度和取消状态，不因新source或浏览器关闭重写原输入。
type Operation struct {
	ID              string     `json:"id"`
	TargetID        string     `json:"targetId"`
	ProjectID       string     `json:"projectId"`
	ServiceID       string     `json:"serviceId"`
	Version         string     `json:"version"`
	State           string     `json:"state"`
	Code            string     `json:"code"`
	Message         string     `json:"message"`
	CancelRequested bool       `json:"cancelRequested"`
	Steps           []Step     `json:"steps"`
	CreatedAt       string     `json:"createdAt"`
	UpdatedAt       string     `json:"updatedAt"`
	Candidate       *Candidate `json:"candidate"`
}

// Receipt 冻结原请求身份与受理结果，不把202或回执当作镜像成功。
type Receipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	MutationID    string `json:"mutationId"`
	Kind          string `json:"kind"`
	OperationID   string `json:"operationId"`
	Target        Target `json:"target"`
	Version       string `json:"version"`
	Replayed      bool   `json:"replayed"`
}

// Snapshot 区分历史成功候选与当前固定输入的可用性，保留原始追溯。
type Snapshot struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	ProjectID        string                `json:"projectId"`
	ServiceID        string                `json:"serviceId"`
	Generation       string                `json:"generation"`
	Target           *Target               `json:"target"`
	Operation        *Operation            `json:"operation"`
	Candidate        *Candidate            `json:"candidate"`
	CandidateCurrent bool                  `json:"candidateCurrent"`
	CandidateCode    string                `json:"candidateCode"`
	CurrentSource    project.SourceVersion `json:"currentSource"`
}

// Capability 只表达显式配置和固定支持组合，不冒称某次构建已经通过。
type Capability struct {
	SchemaVersion  int                  `json:"schemaVersion"`
	Enabled        bool                 `json:"enabled"`
	Adapter        string               `json:"adapter"`
	Code           string               `json:"code"`
	Message        string               `json:"message"`
	Profile        engineering.Identity `json:"profile"`
	ImageReference string               `json:"imageReference"`
	Policy         Policy               `json:"policy"`
}

// Fingerprint 为实际结构化内容计算SHA256，不依赖显示名或可变tag。
func Fingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

// ContentDigest 对真实输出字节计算SHA256，供工具证据与二进制内容共用。
func ContentDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

// Timestamp 保留同库记录的UTC时间和纳秒，不用于替代操作版本。
func Timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
