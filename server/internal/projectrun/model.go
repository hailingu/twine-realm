// Package projectrun 管理原项目停止与恢复的持久父操作。
package projectrun

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"time"
)

// Frozen 固定原目标及物理身份，不以同名替代资源恢复。
type Frozen struct {
	Runtime                 mysqlruntime.Target  `json:"runtime"`
	Deployment              servicedeploy.Target `json:"deployment"`
	DatabaseContainerID     string               `json:"databaseContainerId"`
	ApplicationContainerID  string               `json:"applicationContainerId"`
	DatabaseNetworkID       string               `json:"databaseNetworkId"`
	IngressNetworkID        string               `json:"ingressNetworkId"`
	DatabaseVolumeCreatedAt string               `json:"databaseVolumeCreatedAt"`
	MigrationContainerID    string               `json:"migrationContainerId"`
	ServerUUID              string               `json:"serverUuid"`
}

// Command 保存请求原键与乐观版本，恢复不接受新的候选。
type Command struct {
	MutationID     string `json:"mutationId"`
	BaseRevision   string `json:"baseRevision"`
	BaseGeneration string `json:"baseGeneration"`
	Desired        string `json:"desired"`
}

// Target 保存不可变父目标。
type Target struct {
	ProjectID  string `json:"projectId"`
	Generation string `json:"generation"`
	Desired    string `json:"desired"`
	Frozen     Frozen `json:"frozen"`
	CreatedAt  string `json:"createdAt"`
}

// Step 在外部动作之前登记执行意图，未知结果不能反向覆盖。
type Step struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	ExternalID string `json:"externalId"`
	Code       string `json:"code"`
	UpdatedAt  string `json:"updatedAt"`
}

// Operation 保存父操作及不可遗失的外部动作账本。
type Operation struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	MutationID string `json:"mutationId"`
	Generation string `json:"generation"`
	Version    string `json:"version"`
	State      string `json:"state"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Steps      []Step `json:"steps"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// Receipt 是同库持久的原键受理证据。
type Receipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	MutationID    string `json:"mutationId"`
	OperationID   string `json:"operationId"`
	Target        Target `json:"target"`
	Replayed      bool   `json:"replayed"`
}

// Facts 区分停止事实与仍保留原时间的认证和迁移证明。
type Facts struct {
	ApplicationStopped    bool                       `json:"applicationStopped"`
	DatabaseStopped       bool                       `json:"databaseStopped"`
	ApplicationObservedAt string                     `json:"applicationObservedAt"`
	DatabaseObservedAt    string                     `json:"databaseObservedAt"`
	Runtime               *mysqlruntime.Observation  `json:"runtime"`
	Deployment            *servicedeploy.Observation `json:"deployment"`
}

// Snapshot 返回当前父目标、操作与最后核实事实。
type Snapshot struct {
	SchemaVersion int        `json:"schemaVersion"`
	ProjectID     string     `json:"projectId"`
	Generation    string     `json:"generation"`
	Target        *Target    `json:"target"`
	Operation     *Operation `json:"operation"`
	Facts         *Facts     `json:"facts"`
}

// Lease 绑定父代次和执行身份，不伪造组件租约。
type Lease struct {
	Target    Target
	Operation Operation
	Execution uint64
	Recover   bool
}

// Completion 只有真实核实结果才能提交成功。
type Completion struct {
	State, Code, Message string
	Facts                *Facts
}

// AcceptedInput 提供只读冻结结果，存储在项目锁内重新核对原目标。
type AcceptedInput struct {
	Proof     Facts
	ProjectID string
	Command   Command
	Frozen    Frozen
	Hash      [32]byte
}

// Repository 拥有同事务受理、围栏、账本和通知。
type Repository interface {
	AcceptProjectRun(context.Context, AcceptedInput) (Receipt, error)
	RetryProjectRun(context.Context, string, string) (Receipt, error)
	ProjectRunReceipt(context.Context, string) (Receipt, [32]byte, error)
	ProjectRunSnapshot(context.Context, string) (Snapshot, error)
	ClaimProjectRun(context.Context, time.Time, time.Time) (*Lease, error)
	ProjectRunAuthorized(context.Context, Lease) (bool, error)
	CheckpointProjectRun(context.Context, Lease, Step) (bool, error)
	FinishProjectRun(context.Context, Lease, Completion) error
	YieldProjectRun(context.Context, Lease) error
}

// Adapter 只执行原资源动作，每次外部动作前后必须等待检查点提交。
type Adapter interface {
	Freeze(context.Context, mysqlruntime.Target, servicedeploy.Target) (Frozen, Facts, error)
	StopExisting(context.Context, Lease, func(context.Context, Step) error) (Facts, error)
	ResumeExisting(context.Context, Lease, func(context.Context, Step) error) (Facts, error)
}
