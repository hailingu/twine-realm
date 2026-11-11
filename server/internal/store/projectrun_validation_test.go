// 本文件覆盖父操作前置状态及不可改写账本的失败边界。
package store

import (
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// TestProjectRunPreviousState 恢复只接受完整停止，普通停止只从稳定恢复之后再次发起。
func TestProjectRunPreviousState(t *testing.T) {
	input := projectrun.AcceptedInput{Command: projectrun.Command{Desired: "running"}}
	old := projectrun.Snapshot{Target: &projectrun.Target{Desired: "stopped"}, Operation: &projectrun.Operation{State: "succeeded"}, Facts: &projectrun.Facts{ApplicationStopped: true, DatabaseStopped: true}}
	if err := validatePreviousProjectRun(input, old); err != nil {
		t.Fatal(err)
	}
	if err := validatePreviousProjectRun(input, projectrun.Snapshot{}); err == nil {
		t.Fatal("无停止目标仍恢复")
	}
	old.Facts.DatabaseStopped = false
	if err := validatePreviousProjectRun(input, old); err == nil {
		t.Fatal("部分停止仍恢复")
	}
	input.Command.Desired = "stopped"
	old.Operation.State = "executing"
	if err := validatePreviousProjectRun(input, old); err == nil {
		t.Fatal("在途父操作被覆盖")
	}
	old.Operation.State = "succeeded"
	old.Target.Desired = "running"
	if err := validatePreviousProjectRun(input, old); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunUnsettledComponents 任一组件的在途或未知动作都会阻止聚合受理。
func TestProjectRunUnsettledComponents(t *testing.T) {
	for _, state := range []string{"executing", "unknown"} {
		runtime := mysqlruntime.Snapshot{Operation: &mysqlruntime.Operation{Steps: []mysqlruntime.Step{{State: state}}}}
		deployment := servicedeploy.Snapshot{Operation: &servicedeploy.Operation{}}
		if err := validateSettledProjectComponents(runtime, deployment); err == nil {
			t.Fatal("未结数据库动作被忽略")
		}
		runtime.Operation.Steps = nil
		deployment.Operation.Steps = []servicedeploy.Step{{State: state}}
		if err := validateSettledProjectComponents(runtime, deployment); err == nil {
			t.Fatal("未结业务动作被忽略")
		}
	}
}

// TestProjectRunImmutableLedger 拒绝缺少开始记录、重复成功、未知重写和物理对象改变。
func TestProjectRunImmutableLedger(t *testing.T) {
	for _, state := range []string{"unknown", "succeeded", "executing"} {
		operation := projectrun.Operation{Steps: []projectrun.Step{{Name: "stop_application", State: state, ExternalID: "original"}}}
		point := projectrun.Step{Name: "stop_application", State: "succeeded", ExternalID: "replacement"}
		if err := updateProjectRunStep(&operation, point); err == nil {
			t.Fatal("原动作被改写")
		}
	}
	operation := projectrun.Operation{}
	if err := updateProjectRunStep(&operation, projectrun.Step{Name: "stop_application", State: "succeeded"}); err == nil {
		t.Fatal("缺少执行意图仍成功")
	}
	point := projectrun.Step{Name: "stop_application", State: "executing", ExternalID: "original"}
	if err := updateProjectRunStep(&operation, point); err != nil {
		t.Fatal(err)
	}
	point.State = "succeeded"
	if err := updateProjectRunStep(&operation, point); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunCompletionFailures 无效终态、缺失账本和缺失事实均不能产生成功。
func TestProjectRunCompletionFailures(t *testing.T) {
	lease := projectrun.Lease{Target: projectrun.Target{Desired: "stopped"}}
	for _, state := range []string{"invented", "succeeded"} {
		if err := validateProjectRunCompletion(lease, projectrun.Completion{State: state}); err == nil {
			t.Fatal("无效完成被接受")
		}
	}
	if err := validateProjectRunFacts(lease.Target, nil); err == nil {
		t.Fatal("缺少事实被接受")
	}
	now := time.Now().Format(time.RFC3339Nano)
	if err := validateProjectRunFacts(lease.Target, &projectrun.Facts{ApplicationObservedAt: now, DatabaseObservedAt: now}); err == nil {
		t.Fatal("部分事实被接受")
	}
	if err := validateProjectRunFacts(projectrun.Target{Desired: "running"}, &projectrun.Facts{}); err == nil {
		t.Fatal("缺少恢复证明被接受")
	}
}
