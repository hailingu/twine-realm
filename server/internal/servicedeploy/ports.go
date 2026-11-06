// 本文件定义真实部署消费的存储与执行边界，避免外部I/O进入元数据库事务。
package servicedeploy

import (
	"context"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// AcceptedInput 将已审阅计划与原键摘要送交同库重验。
type AcceptedInput struct {
	Plan    Plan
	Command Command
	Hash    [32]byte
}

// Lease 固定执行代次；到期不意味着旧外部进程已经退出。
type Lease struct {
	Target    Target
	Operation Operation
	Execution uint64
	Recover   bool
}

// Completion 要求真实观察支撑成功，未知外部结果保留原目标。
type Completion struct {
	State, Code, Message string
	Observation          *Observation
}

// RuntimeMaterial 将原物理目标与不可回退的材料完成证明一起读取，不用新操作pending掩盖历史。
type RuntimeMaterial struct {
	Target       Target
	Materialized bool
}

// Repository 同库拥有目标、原键、围栏与最后事实，不拥有Docker事务。
type Repository interface {
	AcceptDeployment(context.Context, AcceptedInput) (Receipt, error)
	DeploymentReceipt(context.Context, string) (Receipt, [32]byte, error)
	DeploymentSnapshot(context.Context, string, string) (Snapshot, error)
	DeploymentOperation(context.Context, string, string, string) (Operation, error)
	ClaimDeployment(context.Context, time.Time, time.Time) (*Lease, error)
	DeploymentAuthorized(context.Context, Lease) (bool, error)
	CheckpointDeployment(context.Context, Lease, Step) (bool, error)
	FinishDeployment(context.Context, Lease, Completion) error
	YieldDeployment(context.Context, Lease) error
	DeploymentTargets(context.Context) ([]Target, error)
	ObserveDeployment(context.Context, Target, *Observation, string, time.Time) error
}

// DeclarationReader 复用已有声明、构建与MySQL读取，不重新定义各自状态来源。
type DeclarationReader interface {
	Graph(context.Context, string) (project.GraphSnapshot, error)
	BuildSnapshot(context.Context, string, string) (servicebuild.Snapshot, error)
	RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error)
}

// Adapter 只执行已冻结目标，准备预览时禁止外部写入。
type Adapter interface {
	Prepare(context.Context, mysqlruntime.Snapshot, servicebuild.Snapshot) (Database, Environment, error)
	Execute(context.Context, Lease, func(context.Context, Step) error) (*Observation, error)
	Inspect(context.Context, Target) (*Observation, error)
}

// Terminal 只有显式新请求才能重新核实终态，后台不盲目重跑迁移。
func Terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "needs_attention"
}
