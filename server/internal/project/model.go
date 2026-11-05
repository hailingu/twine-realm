// Package project 定义本机项目、服务仓库与画布的稳定领域对象。
package project

import (
	"context"
	"crypto/rand"
	"fmt"
)

// Position 表示不依赖任何画布库的节点坐标。
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Repository 保存稳定仓库身份、当前本机定位及登记时可读取的源码版本。
type Repository struct {
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	HeadCommit *string `json:"headCommit"`
}

// Node 是尚未部署的业务服务声明，不包含 React Flow 内部数据。
type Node struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"displayName"`
	Repository  Repository `json:"repository"`
	Position    Position   `json:"position"`
}

// Project 是同一持久化边界读取的完整已确认画布。
type Project struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Revision      string `json:"revision"`
	Nodes         []Node `json:"nodes"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// Summary 是项目入口展示的轻量持久摘要。
type Summary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Revision  string `json:"revision"`
	NodeCount int    `json:"nodeCount"`
	UpdatedAt string `json:"updatedAt"`
}

// CreateCommand 明确一次项目创建的稳定请求身份。
type CreateCommand struct {
	MutationID string `json:"mutationId"`
	Name       string `json:"name"`
}

// RegisterCommand 将已有仓库登记为服务声明，不授权修改源仓库。
type RegisterCommand struct {
	MutationID     string   `json:"mutationId"`
	BaseRevision   string   `json:"baseRevision"`
	RepositoryPath string   `json:"repositoryPath"`
	DisplayName    string   `json:"displayName,omitempty"`
	Position       Position `json:"position"`
}

// NodePosition 把位置变更绑定到稳定的服务身份。
type NodePosition struct {
	ServiceID string  `json:"serviceId"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

// LayoutCommand 在指定画布版本上整批保存位置。
type LayoutCommand struct {
	MutationID   string         `json:"mutationId"`
	BaseRevision string         `json:"baseRevision"`
	Positions    []NodePosition `json:"positions"`
}

// Receipt 是与画布修改同事务提交的回执。
type Receipt struct {
	MutationID    string         `json:"mutationId"`
	Kind          string         `json:"kind"`
	Project       Project        `json:"project"`
	Replayed      bool           `json:"replayed"`
	SourceVersion *SourceVersion `json:"sourceVersion,omitempty"`
	Canvas        *CanvasState   `json:"canvas,omitempty"`
}

// RepositorySource 只包含校验完成后的本机仓库观测。
type RepositorySource struct {
	Path       string
	HeadCommit *string
}

// Reader 提供一致的项目读取边界。
type Reader interface {
	List(context.Context) ([]Summary, error)
	Get(context.Context, string) (Project, error)
	Source(context.Context, string, string) (SourceVersion, error)
	Canvas(context.Context, string) (CanvasSnapshot, error)
}

// Writer 原子提交领域修改及对应回执。
type Writer interface {
	Create(context.Context, CreateCommand, [32]byte) (Receipt, error)
	Register(context.Context, string, RegisterCommand, RepositorySource, [32]byte) (Receipt, error)
	SaveLayout(context.Context, string, LayoutCommand, [32]byte) (Receipt, error)
	PinSource(context.Context, string, string, PinSourceCommand, [32]byte) (Receipt, error)
	PutMiddleware(context.Context, string, string, MiddlewareCommand, []string, [32]byte) (Receipt, error)
	SaveCanvasLayout(context.Context, string, CanvasLayoutCommand, [32]byte) (Receipt, error)
}

// ReceiptReader 查询原始结果与请求摘要，用于结果未知时的恢复。
type ReceiptReader interface {
	Receipt(context.Context, string) (Receipt, [32]byte, error)
}

// Inspector 只读校验允许目录内的真实 Git 工作区。
type Inspector interface {
	Inspect(context.Context, string) (RepositorySource, error)
	Observe(context.Context, string, *string) (GitObservation, error)
	VerifyCommit(context.Context, string, string) error
}

// NewID 产生与名称、路径和版本无关的 UUID，不复用已删除实体的身份。
func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成实体身份: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
