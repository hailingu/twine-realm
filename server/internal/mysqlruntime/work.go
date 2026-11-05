// 本文件定义持久领取与步骤检查点，不把租约到期误认为外部命令已经停止。
package mysqlruntime

import (
	"context"
	"time"
)

// Lease 将执行代次绑定到原操作；它不是 Docker 提供的事务锁。
type Lease struct {
	Target          Target
	Operation       Operation
	Execution       string
	VolumeConfirmed bool
	ServerUUID      string
}

// Checkpoint 在副作用前后持久记录步骤，已替代结果只能保留在原操作。
type Checkpoint struct {
	Name            string
	State           string
	Code            string
	ExternalID      string
	VolumeConfirmed bool
}

// Completion 保存实际结果和恢复入口；Observation 只在仍是当前目标时应用。
type Completion struct {
	State       string
	Code        string
	Message     string
	StepCode    string
	NextAttempt time.Time
	Observation *Observation
}

// QueueStore 提供有条件的持久领取、检查点和结算，不在事务内执行 Docker。
type QueueStore interface {
	ClaimRuntime(context.Context, time.Time, time.Time) (*Lease, error)
	CheckpointRuntime(context.Context, Lease, Checkpoint) (bool, error)
	FinishRuntime(context.Context, Lease, Completion) error
}

// Instance 是观测扫描项；它不授予自动修复已成功资源漂移的权限。
type Instance struct {
	Target          Target
	OperationState  string
	VolumeConfirmed bool
	ServerUUID      string
}

// ObservationStore 只更新条件匹配的当前目标，失败仍保留最后事实及时间。
type ObservationStore interface {
	RuntimeInstances(context.Context, string) ([]Instance, error)
	ObserveRuntime(context.Context, Target, *Observation, string, time.Time) error
}

// Policy 固定首版参数，测试可以显式缩短受控期限但不能通过环境关闭检查。
type Policy struct {
	Poll, ObserveEvery, ReadinessTimeout, LeaseDuration time.Duration
	MaxAttempts                                         int
}

// DefaultPolicy 采用设计初值，租约覆盖最长有界执行；项目文件锁另行防止并发写。
func DefaultPolicy() Policy {
	return Policy{time.Second, 5 * time.Second, 120 * time.Second, 180 * time.Second, 6}
}
