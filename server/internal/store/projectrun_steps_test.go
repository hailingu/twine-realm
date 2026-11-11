// 本文件覆盖固定父步骤顺序与未知保护的纯状态负例。
package store

import (
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// TestProjectRunStrictSteps 拒绝越序、替换物理对象及同时推进多个未结动作。
func TestProjectRunStrictSteps(t *testing.T) {
	lease := projectrun.Lease{Target: projectrun.Target{Desired: "running", Frozen: projectrun.Frozen{DatabaseContainerID: "db", ApplicationContainerID: "app"}}}
	cases := []struct {
		name  string
		step  projectrun.Step
		steps []projectrun.Step
	}{
		{"数据库未核实先启动应用", projectrun.Step{Name: "start_application", ExternalID: "app", State: "executing"}, []projectrun.Step{{Name: "start_database", State: "succeeded"}}},
		{"未知步骤阻止后续", projectrun.Step{Name: "verify_database", ExternalID: "db", State: "executing"}, []projectrun.Step{{Name: "start_database", State: "unknown"}}},
		{"同名替代容器拒绝", projectrun.Step{Name: "start_database", ExternalID: "replacement", State: "executing"}, nil},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			lease.Operation.Steps = item.steps
			if err := validateProjectRunStep(lease, item.step); err == nil {
				t.Fatal("非法步骤未拒绝")
			}
		})
	}
	lease.Operation.Steps = []projectrun.Step{{Name: "start_database", State: "succeeded", ExternalID: "db"}, {Name: "verify_database", State: "succeeded", ExternalID: "db"}, {Name: "start_application", State: "succeeded", ExternalID: "app"}}
	if err := completeProjectRunSteps(lease); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunStopFactTime 拒绝空白、无效及未来停止证明。
func TestProjectRunStopFactTime(t *testing.T) {
	for _, stamp := range []string{"", "invalid", time.Now().Add(time.Hour).Format(time.RFC3339Nano)} {
		facts := &projectrun.Facts{ApplicationStopped: true, DatabaseStopped: true, ApplicationObservedAt: stamp, DatabaseObservedAt: stamp}
		if err := validateProjectRunFacts(projectrun.Target{Desired: "stopped"}, facts); err == nil {
			t.Fatal("无效停止时间未拒绝")
		}
	}
}
