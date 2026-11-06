// 本文件定义构建用例实际消费的持久端口，执行器不持有数据库连接。
package servicebuild

import (
	"context"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// AcceptedInput 由用例核实工具与源码，存储在事务内重新核实关联及版本。
type AcceptedInput struct {
	Target  Target
	Command Command
	Hash    [32]byte
}

// Commands 拥有原键与同库受理，未知提交不能授权外部构建。
type Commands interface {
	BuildReceipt(context.Context, string) (Receipt, [32]byte, error)
	AcceptBuild(context.Context, AcceptedInput) (Receipt, error)
	CancelBuild(context.Context, string, string, string, CancelCommand, [32]byte) (Receipt, error)
}

// Queries 返回完整已提交快照和历史操作，不以Docker观察代替元数据。
type Queries interface {
	BuildSnapshot(context.Context, string, string) (Snapshot, error)
	BuildOperation(context.Context, string, string, string) (Operation, error)
}

// SourceReader 提供当前持久关联，避免构建端自行查询或更改Git选择。
type SourceReader interface {
	Source(context.Context, string, string) (project.SourceVersion, error)
}

// CommitVerifier 只核实固定完整commit确实属于原关联仓库。
type CommitVerifier interface {
	VerifyCommit(context.Context, string, string) error
}

// Lease 的执行代次和到期时间只由存储拥有，浏览器不能修改。
type Lease struct {
	Target    Target
	Operation Operation
	Execution uint64
	Recover   bool
}

// Completion 只在沙箱收回后提交，失败或取消均不得携带候选。
type Completion struct {
	State, Code, Message string
	Candidate            *Candidate
}

// Queue 提供一次领取、执行授权核实及终态围栏。
type Queue interface {
	ClaimBuild(context.Context, time.Time, time.Time) (*Lease, error)
	CheckpointBuild(context.Context, Lease, Step) (bool, error)
	FinishBuild(context.Context, Lease, Completion) error
}

// Authorization 让长命令期间的取消及时收回沙箱，而非等下一检查点。
type Authorization interface {
	BuildAuthorized(context.Context, Lease) (bool, error)
}

// Terminal 明确本切片终态；needs_attention不能自动重跑任意业务代码。
func Terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled" || state == "needs_attention"
}
