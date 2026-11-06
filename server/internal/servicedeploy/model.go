// Package servicedeploy 拥有固定候选到单服务MySQL运行的声明、持久操作与安全事实。
package servicedeploy

import (
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// PreviewRequest 只描述受控运行参数，不接受SQL、命令或秘密。
type PreviewRequest struct {
	BaseRevision   string         `json:"baseRevision"`
	BaseGeneration string         `json:"baseGeneration"`
	CandidateID    string         `json:"candidateId"`
	BindingID      string         `json:"bindingId"`
	HostPort       int            `json:"hostPort"`
	Configuration  map[string]any `json:"configuration"`
}

// Command 重新核实用户已审阅的预览，不允许原键换意图或绕过原部署恢复。
type Command struct {
	MutationID string         `json:"mutationId"`
	Preview    PreviewRequest `json:"preview"`
	PlanDigest string         `json:"planDigest"`
	RetryOf    string         `json:"retryOf,omitempty"`
}

// Database 固定已有MySQL实例、数据身份及私网，不含管理凭据或宿主秘密位置。
type Database struct {
	ResourceID        string `json:"resourceId"`
	InstallationID    string `json:"installationId"`
	DaemonID          string `json:"daemonId"`
	ComponentID       string `json:"componentId"`
	Generation        string `json:"generation"`
	ResourceDigest    string `json:"resourceDigest"`
	ConfigurationHash string `json:"configurationHash"`
	ServerUUID        string `json:"serverUuid"`
	Network           string `json:"network"`
	NetworkCIDR       string `json:"networkCidr"`
	Volume            string `json:"volume"`
	Container         string `json:"container"`
	Image             string `json:"image"`
}

// Environment 绑定运行安装与非root用户，不能由普通请求指定任意宿主身份。
type Environment struct {
	Context               string `json:"context"`
	DaemonID              string `json:"daemonId"`
	DockerVersion         string `json:"dockerVersion"`
	ComposeVersion        string `json:"composeVersion"`
	Architecture          string `json:"architecture"`
	BindSourceMode        string `json:"bindSourceMode"`
	UID                   int    `json:"uid"`
	GID                   int    `json:"gid"`
	BuildInstallationID   string `json:"buildInstallationId"`
	RuntimeInstallationID string `json:"runtimeInstallationId"`
}

// Resources 的稳定物理名称不依赖可变显示名或HTTP请求身份。
type Resources struct {
	ComposeProject  string `json:"composeProject"`
	Application     string `json:"application"`
	Migration       string `json:"migration"`
	IngressNetwork  string `json:"ingressNetwork"`
	ApplicationUser string `json:"applicationUser"`
	MigrationUser   string `json:"migrationUser"`
	AccountHost     string `json:"accountHost"`
}

// Plan 同时给出精确预览摘要与运行内容摘要；布局并发版本不成为物理资源的新身份。
type Plan struct {
	SchemaVersion int                     `json:"schemaVersion"`
	ProjectID     string                  `json:"projectId"`
	ServiceID     string                  `json:"serviceId"`
	ServiceName   string                  `json:"serviceName"`
	Request       PreviewRequest          `json:"request"`
	Contract      project.DefinitionRef   `json:"contract"`
	Binding       project.DatabaseBinding `json:"binding"`
	Candidate     servicebuild.Candidate  `json:"candidate"`
	Database      Database                `json:"database"`
	Environment   Environment             `json:"environment"`
	Policy        Policy                  `json:"policy"`
	Resources     Resources               `json:"resources"`
	RuntimeDigest string                  `json:"runtimeDigest"`
	Digest        string                  `json:"digest"`
}

// Target 将已接受预览冻结为不可变运行目标，不把画布保存当部署。
type Target struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	ServiceID  string `json:"serviceId"`
	Generation string `json:"generation"`
	Plan       Plan   `json:"plan"`
	CreatedAt  string `json:"createdAt"`
}

// Step 记录有界外部阶段，未知结果不通过重启伪装为未开始。
type Step struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Code      string `json:"code"`
	UpdatedAt string `json:"updatedAt"`
}

// Operation 的版本只描述原操作，不替代目标代次、画布revision或事件sequence。
type Operation struct {
	ID        string `json:"id"`
	TargetID  string `json:"targetId"`
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	Version   string `json:"version"`
	State     string `json:"state"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Steps     []Step `json:"steps"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Receipt 是同事务持久受理回执，不能被解释为服务已经就绪。
type Receipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	MutationID    string `json:"mutationId"`
	OperationID   string `json:"operationId"`
	Target        Target `json:"target"`
	Replayed      bool   `json:"replayed"`
}

// Observation 分别保留真实进程、存活、就绪与迁移身份，不复制业务返回内容。
type Observation struct {
	State              string `json:"state"`
	Running            bool   `json:"running"`
	Healthy            bool   `json:"healthy"`
	Ready              bool   `json:"ready"`
	ContainerID        string `json:"containerId"`
	ImageID            string `json:"imageId"`
	EntryURL           string `json:"entryUrl"`
	DatabaseUUID       string `json:"databaseUuid"`
	MigrationVersion   uint64 `json:"migrationVersion"`
	MigrationIntegrity string `json:"migrationIntegrity"`
	ObservedAt         string `json:"observedAt"`
}

// Snapshot 保留最后事实与本次观测错误，禁用执行器仍可查询历史。
type Snapshot struct {
	SchemaVersion   int          `json:"schemaVersion"`
	ProjectID       string       `json:"projectId"`
	ServiceID       string       `json:"serviceId"`
	Generation      string       `json:"generation"`
	Sequence        string       `json:"sequence"`
	Target          *Target      `json:"target"`
	Operation       *Operation   `json:"operation"`
	Observation     *Observation `json:"observation"`
	Freshness       string       `json:"freshness"`
	ObservationCode string       `json:"observationCode"`
	LastAttemptAt   string       `json:"lastAttemptAt"`
}

// Capability 只描述显式配置，不能替代实际Docker或数据库验证。
type Capability struct {
	SchemaVersion int    `json:"schemaVersion"`
	Enabled       bool   `json:"enabled"`
	Adapter       string `json:"adapter"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Policy        Policy `json:"policy"`
}
