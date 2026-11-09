// 本文件约束父账本的固定动作顺序与原物理对象，不接受任意命令名称。
package store

import (
	"fmt"
	"slices"

	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// validateProjectRunStep 数据库停止必须晚于应用停止，应用启动必须晚于数据库核实。
func validateProjectRunStep(lease projectrun.Lease, step projectrun.Step) error {
	f := lease.Target.Frozen
	names := []string{"stop_application", "stop_migration", "stop_database"}
	ids := []string{f.ApplicationContainerID, f.MigrationContainerID, f.DatabaseContainerID}
	if lease.Target.Desired == "running" {
		names = []string{"start_database", "verify_database", "start_application"}
		ids = []string{f.DatabaseContainerID, f.DatabaseContainerID, f.ApplicationContainerID}
	}
	index := slices.Index(names, step.Name)
	if index < 0 || ids[index] == "" || ids[index] != step.ExternalID {
		return fmt.Errorf("父动作不属于原冻结资源")
	}
	for _, old := range lease.Operation.Steps {
		if old.Name != step.Name && (old.State == "executing" || old.State == "unknown") {
			return fmt.Errorf("先核实未结父动作")
		}
	}
	for _, required := range names[:index] {
		if !projectRunStepSucceeded(lease.Operation.Steps, required) {
			return fmt.Errorf("父动作前置阶段尚未成功")
		}
	}
	return nil
}

// completeProjectRunSteps 不以仅有终态事实绕过父动作账本。
func completeProjectRunSteps(lease projectrun.Lease) error {
	names := []string{"stop_application", "stop_migration", "stop_database"}
	if lease.Target.Desired == "running" {
		names = []string{"start_database", "verify_database", "start_application"}
	}
	for _, name := range names {
		found := false
		for _, step := range lease.Operation.Steps {
			if step.Name == name && step.State == "succeeded" {
				if err := validateProjectRunStep(lease, step); err != nil {
					return err
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("父操作缺少完整成功账本")
		}
	}
	return nil
}

// projectRunStepSucceeded 查询指定阶段的持久成功证据。
func projectRunStepSucceeded(steps []projectrun.Step, name string) bool {
	for _, step := range steps {
		if step.Name == name && step.State == "succeeded" {
			return true
		}
	}
	return false
}
