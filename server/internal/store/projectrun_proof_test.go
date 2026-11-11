// 本文件验证主动冻结证明，锁竞争导致的被动unknown不替代本次完整采样。
package store

import (
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// TestProjectRunActiveFreezeProof 本次证明必须绑定物理容器、原UUID、候选和完整就绪。
func TestProjectRunActiveFreezeProof(t *testing.T) {
	target, deployment := deploymentFactFixture(t)
	target.Plan.Request.HostPort = 8081
	now := time.Now().UTC()
	deployment.ObservedAt = now.Format(time.RFC3339Nano)
	frozen := projectrun.Frozen{Deployment: target, DatabaseContainerID: "database", ApplicationContainerID: deployment.ContainerID, ServerUUID: deployment.DatabaseUUID}
	runtime := mysqlruntime.Observation{State: "running", Running: true, Ready: true, VolumeFound: true, ContainerID: "database", ServerUUID: frozen.ServerUUID, ObservedAt: now.Format(time.RFC3339Nano)}
	proof := projectrun.Facts{Runtime: &runtime, Deployment: &deployment}
	if err := validateProjectRunProof(frozen, proof, now); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name   string
		change func(*projectrun.Facts)
	}{
		{"旧证明", func(p *projectrun.Facts) { p.Runtime.ObservedAt = now.Add(-21 * time.Second).Format(time.RFC3339Nano) }},
		{"替换容器", func(p *projectrun.Facts) { p.Deployment.ContainerID = "replacement" }},
		{"错误UUID", func(p *projectrun.Facts) { p.Runtime.ServerUUID = "replacement" }},
		{"dirty迁移", func(p *projectrun.Facts) { p.Deployment.MigrationIntegrity = "dirty" }},
		{"缺失认证", func(p *projectrun.Facts) { p.Runtime.Ready = false }},
	} {
		t.Run(item.name, func(t *testing.T) {
			r, d := runtime, deployment
			value := projectrun.Facts{Runtime: &r, Deployment: &d}
			item.change(&value)
			if err := validateProjectRunProof(frozen, value, now); err == nil {
				t.Fatal("不完整证明被接受")
			}
		})
	}
	if err := validateProjectRunProof(frozen, projectrun.Facts{}, now); err == nil {
		t.Fatal("没有本次证明仍受理")
	}
}
