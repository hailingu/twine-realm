// 本文件只描述真实检查后发布的镜像候选，不包含部署许可或运行秘密。
package servicebuild

import "github.com/hailingu/twine-realm/server/internal/engineering"

// Check 记录已完成的真实工具检查，失败检查不会成为候选证据。
type Check struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	ToolVersion  string `json:"toolVersion"`
	OutputDigest string `json:"outputDigest"`
	CompletedAt  string `json:"completedAt"`
}

// Migration 固定源码内实际迁移文件的相对位置和内容摘要。
type Migration struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Image 记录实际本地身份与工具提供的内容证据；缺失的独立摘要不补造。
type Image struct {
	ID                string `json:"id"`
	IDKind            string `json:"idKind"`
	ExportDigest      string `json:"exportDigest"`
	ManifestDigest    string `json:"manifestDigest,omitempty"`
	ManifestMediaType string `json:"manifestMediaType,omitempty"`
	ConfigDigest      string `json:"configDigest,omitempty"`
	Tag               string `json:"tag"`
	BinaryDigest      string `json:"binaryDigest"`
}

// Candidate 将检查、源码、工程包、工具链、策略与镜像绑定到同一不可变输入。
type Candidate struct {
	ID              string               `json:"id"`
	TargetID        string               `json:"targetId"`
	OperationID     string               `json:"operationId"`
	InputDigest     string               `json:"inputDigest"`
	SourceDigest    string               `json:"sourceDigest"`
	Source          Source               `json:"source"`
	Profile         engineering.Identity `json:"profile"`
	Template        engineering.Identity `json:"template"`
	Environment     Environment          `json:"environment"`
	Policy          Policy               `json:"policy"`
	Checks          []Check              `json:"checks"`
	Migrations      []Migration          `json:"migrations"`
	MigrationDigest string               `json:"migrationDigest"`
	Image           Image                `json:"image"`
	CreatedAt       string               `json:"createdAt"`
}
