// 本文件验证用途和受保护元数据的权限映射，不把应用readOnly降级成读写。
package mysqlruntime

import (
	"reflect"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// TestApplicationPrivilegesRespectPurpose 业务表按保存用途授权，所有历史与身份表始终仅可读。
func TestApplicationPrivilegesRespectPurpose(t *testing.T) {
	for _, purpose := range []string{"readOnly", "readWrite"} {
		session := BindingSession{request: BindingRequest{Binding: project.DatabaseBinding{Purpose: purpose}}}
		for _, table := range []string{"todos", "schema_migrations", "service_migration_digests", "twine_binding_identity"} {
			want := []string{"SELECT"}
			if purpose == "readWrite" && table == "todos" {
				want = applicationPrivileges
			}
			if !reflect.DeepEqual(session.applicationTablePrivileges(table), want) {
				t.Fatal("用途或受保护元数据权限被扩大")
			}
		}
	}
}
