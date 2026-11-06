// 本文件回归真实Docker发现的固定PATH，并拒绝任意额外环境、挂载和权限放宽。
package servicecompose

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// structuralPlan 使用真实公开预览的固定材料；此处不访问原仓库或Docker。
func structuralPlan(t *testing.T) servicedeploy.Plan {
	t.Helper()
	data, err := os.ReadFile("../../contract/v4/fixtures/preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var value servicedeploy.Plan
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestDockerDefaultPATHRemainsExact 固定scratch容器实际带Docker默认PATH，显式声明后仍拒绝外部改写。
func TestDockerDefaultPATHRemainsExact(t *testing.T) {
	p := structuralPlan(t)
	expected := environment(p, "migration")
	actual := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	for key, value := range expected {
		if key != "PATH" {
			actual = append(actual, key+"="+value)
		}
	}
	if !validEnvironment(actual, expected) {
		t.Fatal("真实Docker固定默认PATH被当成未知额外环境")
	}
	for _, extra := range []string{"PATH=/unsafe", "DB_PASSWORD=synthetic-private", "MIGRATION_USER=foreign"} {
		if validEnvironment(append(append([]string{}, actual...), extra), expected) {
			t.Fatal("附加或重复环境被接受")
		}
	}
	actual[0] = "PATH=/unsafe"
	if validEnvironment(actual, expected) {
		t.Fatal("被替换PATH仍被接受")
	}
}

// TestRoleMaterialAndIsolation 渲染内容不含管理秘密，运行隔离的真实字段不能被放宽。
func TestRoleMaterialAndIsolation(t *testing.T) {
	p := structuralPlan(t)
	files := mysqlruntime.BindingFiles{Application: "/private/test/app.secret", Migration: "/private/test/migration.secret"}
	data, err := composeMaterial(p, files)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"root.secret", "root_password", "docker.sock", "/source", "MYSQL_ROOT_PASSWORD"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("业务材料包含越界引用")
		}
	}
	application := environment(p, "application")
	if application["MIGRATION_USER"] != "" || application["DB_PASSWORD_FILE"] != "/run/secrets/application_password" {
		t.Fatal("角色秘密未隔离")
	}
	if _, err := migrationDigests(p.Candidate); err != nil {
		t.Fatal(err)
	}
	for _, integrity := range []string{"dirty", "mismatch", "unknown"} {
		if historyFailure(mysqlruntime.MigrationHistory{Integrity: integrity}) == nil {
			t.Fatal("不完整历史被允许重新迁移")
		}
	}
	if historyFailure(mysqlruntime.MigrationHistory{Integrity: "missing"}) != nil {
		t.Fatal("全新空历史不能首次迁移")
	}
	if equalPorts(map[string][]portBinding{"8080/tcp": {{HostIP: "0.0.0.0", HostPort: "8081"}}}, map[string][]portBinding{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "8081"}}}) {
		t.Fatal("公开端口被当成本机端口")
	}
}
