// Package projectstate 定义同库一致信封与有界状态通知，不拥有业务执行或编辑确认。
package projectstate

import (
	"context"
	"regexp"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// Policy 是首个状态运输版本的固定边界，不从浏览器接受覆盖值。
type Policy struct {
	HeartbeatSeconds      int `json:"heartbeatSeconds"`
	IdleSeconds           int `json:"idleSeconds"`
	StaleSeconds          int `json:"staleSeconds"`
	RetentionSeconds      int `json:"retentionSeconds"`
	MaxEvents             int `json:"maxEvents"`
	PageSize              int `json:"pageSize"`
	MaxFrameBytes         int `json:"maxFrameBytes"`
	MaxSnapshotBytes      int `json:"maxSnapshotBytes"`
	MaxConnections        int `json:"maxConnections"`
	MaxProjectConnections int `json:"maxProjectConnections"`
	RetrySeconds          int `json:"retrySeconds"`
}

// DefaultPolicy 固定实际实现并经验证的参数；更改须发布对应契约。
func DefaultPolicy() Policy {
	return Policy{15, 45, 20, 86400, 100000, 200, 4096, 64 * 1024 * 1024, 16, 4, 2}
}

// Snapshot 的所有持久字段由同一元数据库读屏障生成，外部事实仍保留各自采集时间。
type Snapshot struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Project       project.Project          `json:"project"`
	Graph         project.GraphState       `json:"graph"`
	Runtime       mysqlruntime.Snapshot    `json:"runtime"`
	Builds        []servicebuild.Snapshot  `json:"builds"`
	Deployments   []servicedeploy.Snapshot `json:"deployments"`
	Cursor        string                   `json:"cursor"`
	ReplayFloor   string                   `json:"replayFloor"`
	CapturedAt    string                   `json:"capturedAt"`
	Policy        Policy                   `json:"policy"`
}

// Reference 关联通知来源的目标、操作和执行版本；它不代表操作已完成。
type Reference struct {
	ServiceID        string `json:"serviceId,omitempty"`
	Generation       string `json:"generation"`
	OperationID      string `json:"operationId"`
	OperationVersion string `json:"operationVersion"`
	Execution        string `json:"execution"`
}

// Event 只通知核实持久状态，不能用它确认或再次应用用户编辑。
type Event struct {
	SchemaVersion int        `json:"schemaVersion"`
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	Sequence      string     `json:"sequence"`
	Scope         string     `json:"scope"`
	Revision      string     `json:"revision"`
	Reference     *Reference `json:"reference"`
	OccurredAt    string     `json:"occurredAt"`
}

// Page 是有界读取结果，Head 与游标在同一个读屏障内核实。
type Page struct {
	Events     []Event
	Next, Head string
	HasMore    bool
}

// CaughtUp 是独立控制帧，不占用或递增业务事件序号。
type CaughtUp struct {
	SchemaVersion int    `json:"schemaVersion"`
	ProjectID     string `json:"projectId"`
	Cursor        string `json:"cursor"`
}

// Resync 指示当前流不能继续重放，客户端必须重新取得完整信封。
type Resync struct {
	SchemaVersion int    `json:"schemaVersion"`
	ProjectID     string `json:"projectId"`
	Code          string `json:"code"`
}

// Reader 只消费同库快照和通知，不依赖 Docker、Git 或浏览器寿命。
type Reader interface {
	ProjectState(context.Context, string) (Snapshot, error)
	ProjectEvents(context.Context, string, string) (Page, error)
}

// Service 在统一边界校验项目和游标，不因断流创建或取消持久操作。
type Service struct{ reader Reader }

// NewService 装配唯一持久读取端口。
func NewService(reader Reader) *Service { return &Service{reader: reader} }

// Snapshot 取得完整信封；不存在项目和存储失败保持显式错误。
func (service *Service) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	if !project.ValidID(id) {
		return Snapshot{}, project.Failure("INVALID_REQUEST", "项目身份无效")
	}
	return service.reader.ProjectState(ctx, id)
}

var cursorPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)

// Cursor 将规范十进制游标转为完整 uint64，不经 JavaScript 浮点数。
func Cursor(value string) (uint64, error) {
	if !cursorPattern.MatchString(value) {
		return 0, project.Failure("INVALID_REQUEST", "事件游标必须是规范十进制字符串")
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, project.Failure("INVALID_REQUEST", "事件游标超出版本范围")
	}
	return number, nil
}

// Events 校验显式游标后读取一页；窗口失效不伪装为空历史。
func (service *Service) Events(ctx context.Context, id, after string) (Page, error) {
	if !project.ValidID(id) {
		return Page{}, project.Failure("INVALID_REQUEST", "项目身份无效")
	}
	if _, err := Cursor(after); err != nil {
		return Page{}, err
	}
	return service.reader.ProjectEvents(ctx, id, after)
}

// Timestamp 统一通知的 UTC 时间表示。
func Timestamp(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
