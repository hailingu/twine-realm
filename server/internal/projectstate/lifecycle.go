// 本文件扩展版本化读取，不改变严格旧信封。
package projectstate

import "github.com/hailingu/twine-realm/server/internal/projectrun"

// SnapshotV6 在同一读取事务包含旧状态与父生命周期。
type SnapshotV6 struct {
	SchemaVersion int                 `json:"schemaVersion"`
	State         Snapshot            `json:"state"`
	Lifecycle     projectrun.Snapshot `json:"lifecycle"`
}
