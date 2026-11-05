// Package mysqlruntime 管理首个本机 MySQL 的持久目标、恢复与受控 Compose 边界。
package mysqlruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Image 固定目录已经发布的镜像，不接受请求指定任意 tag 或仓库。
const Image = "mysql:9.7.4@sha256:df90258da46f6e7ff8574ff84f17618aba0765cac749928299c69fd4e7cb8ad8"

// Command 明确接受一个新目标；同键重放先核实原结果，不重新操作 Docker。
type Command struct {
	MutationID     string `json:"mutationId"`
	BaseRevision   string `json:"baseRevision"`
	BaseGeneration string `json:"baseGeneration"`
	Desired        string `json:"desired"`
	RetryOf        string `json:"retryOf,omitempty"`
}

// Resource 固定运行实例的归属与配置；它不含密码、宿主秘密路径或用户 SQL。
type Resource struct {
	ID                string                `json:"id"`
	InstallationID    string                `json:"installationId"`
	DaemonID          string                `json:"daemonId"`
	ProjectID         string                `json:"projectId"`
	ComponentID       string                `json:"componentId"`
	Definition        project.DefinitionRef `json:"definition"`
	Configuration     map[string]any        `json:"configuration"`
	ConfigurationHash string                `json:"configurationHash"`
	Image             string                `json:"image"`
	Network           string                `json:"network"`
	Volume            string                `json:"volume"`
	Container         string                `json:"container"`
	SecretRef         string                `json:"secretRef"`
}

// Target 是受理时固定的不可变目标；generation 不替代画布 revision。
type Target struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"projectId"`
	CanvasRevision string   `json:"canvasRevision"`
	Generation     string   `json:"generation"`
	Desired        string   `json:"desired"`
	Resource       Resource `json:"resource"`
	CreatedAt      string   `json:"createdAt"`
}

// Step 保存先登记后执行的步骤与未知结果，外部身份只取受控资源计划。
type Step struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Code       string `json:"code"`
	ExternalID string `json:"externalId"`
	UpdatedAt  string `json:"updatedAt"`
}

// Operation 的状态和 version 独立于目标和事件，终态不通过同请求重放重开。
type Operation struct {
	MutationID string `json:"mutationId"`
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	Generation string `json:"generation"`
	State      string `json:"state"`
	Version    string `json:"version"`
	Attempts   int    `json:"attempts"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RetryOf    string `json:"retryOf"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Steps      []Step `json:"steps"`
}

// Receipt 是同事务保存的受理回执，不把可变化的执行结果塞入幂等响应。
type Receipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	MutationID    string `json:"mutationId"`
	OperationID   string `json:"operationId"`
	Target        Target `json:"target"`
	Replayed      bool   `json:"replayed"`
}

// Observation 只记录已核实的非敏感事实，ready 必须来自真正 MySQL 认证查询。
type Observation struct {
	State       string `json:"state"`
	Running     bool   `json:"running"`
	Ready       bool   `json:"ready"`
	ServerUUID  string `json:"serverUuid"`
	ContainerID string `json:"containerId"`
	VolumeFound bool   `json:"volumeFound"`
	ObservedAt  string `json:"observedAt"`
}

// Snapshot 保留最后事实与本次采集错误，未知或过期不能继续显示实时健康。
type Snapshot struct {
	SchemaVersion   int          `json:"schemaVersion"`
	ProjectID       string       `json:"projectId"`
	Generation      string       `json:"generation"`
	Sequence        string       `json:"sequence"`
	Target          *Target      `json:"target"`
	Operation       *Operation   `json:"operation"`
	Observation     *Observation `json:"observation"`
	Freshness       string       `json:"freshness"`
	ObservationCode string       `json:"observationCode"`
	LastAttemptAt   string       `json:"lastAttemptAt"`
}

// Event 提供有序、可分页查询的持久进展；本切片不发布 SSE。
type Event struct {
	Sequence    string `json:"sequence"`
	OperationID string `json:"operationId"`
	Generation  string `json:"generation"`
	Kind        string `json:"kind"`
	Code        string `json:"code"`
	CreatedAt   string `json:"createdAt"`
}

// EventPage 限定单次读取，浏览器刷新可以按原游标继续核实。
type EventPage struct {
	SchemaVersion int     `json:"schemaVersion"`
	Events        []Event `json:"events"`
	NextSequence  string  `json:"nextSequence"`
	HasMore       bool    `json:"hasMore"`
}

// Environment 固定初始化时核实的本地 Docker 身份和镜像内容身份。
type Environment struct {
	Context        string `json:"context"`
	DaemonID       string `json:"daemonId"`
	ImageID        string `json:"imageId"`
	ComposeVersion string `json:"composeVersion"`
	BindSourceMode string `json:"bindSourceMode"`
}

// AcceptedInput 是已按目录核实的候选；仓储仍在同事务锁定画布与运行代次。
type AcceptedInput struct {
	ProjectID   string
	ComponentID string
	Command     Command
	Hash        [32]byte
	Resource    Resource
}

// CommandStore 接受目标、恢复原回执和核对安装身份，均不执行外部 I/O。
type CommandStore interface {
	AcceptRuntime(context.Context, AcceptedInput) (Receipt, error)
	RuntimeReceipt(context.Context, string) (Receipt, [32]byte, error)
	BindRuntime(context.Context, string, string) error
}

// QueryStore 读取一致运行快照、原操作和有序事件。
type QueryStore interface {
	RuntimeSnapshot(context.Context, string) (Snapshot, error)
	RuntimeOperation(context.Context, string, string) (Operation, error)
	RuntimeEvents(context.Context, string, string) (EventPage, error)
}

// CanvasReader 只读取已确认声明，草稿和运行态各自拥有版本。
type CanvasReader interface {
	Canvas(context.Context, string) (project.CanvasSnapshot, error)
}

// Definitions 保持目录为最终配置裁决，不从请求或文件执行自定义定义。
type Definitions interface {
	Normalize(project.DefinitionRef, map[string]any) (map[string]any, []string, error)
}

// Terminal 表明原操作不再被后台重新领取；显式恢复须接受新请求。
func Terminal(state string) bool {
	switch state {
	case "needs_attention", "succeeded", "failed", "superseded":
		return true
	default:
		return false
	}
}

// Number 校验不带前导零且不溢出的独立版本号。
func Number(value string) bool {
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(number, 10) == value
}

// Fingerprint 对非敏感规范 JSON 固定摘要，map 键由标准编码器排序。
func Fingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

// Timestamp 统一输出 UTC 时间，不把数据库驱动时间格式暴露为契约。
func Timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
