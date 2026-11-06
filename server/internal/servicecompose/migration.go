// 本文件只启动一次固定迁移任务，已退出任务和未知历史不得通过重跑DDL修复。
package servicecompose

import (
	"context"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// migrationDigests 对实际up文件按模板同一版本规则映射，不把down摘要冒作已应用内容。
func migrationDigests(candidate servicebuild.Candidate) (map[uint64]string, error) {
	result := map[uint64]string{}
	for _, file := range candidate.Migrations {
		if !strings.HasSuffix(file.Path, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(path.Base(file.Path), "_")
		version, err := strconv.ParseUint(prefix, 10, 32)
		if !ok || err != nil || version == 0 || result[version] != "" || !strings.HasPrefix(file.Digest, "sha256:") {
			return nil, servicedeploy.Failure("DEPLOY_MIGRATION_LIST_INVALID", "候选迁移清单不符合已选工程组合", false)
		}
		result[version] = strings.TrimPrefix(file.Digest, "sha256:")
	}
	if len(result) == 0 || len(result) > 64 {
		return nil, servicedeploy.Failure("DEPLOY_MIGRATION_LIST_INVALID", "候选缺少有界的up迁移清单", false)
	}
	return result, nil
}

// migrate 先看原任务，再核实数据库锁和历史，已成功迁移从不再次启动任务。
func (work *execution) migrate(ctx context.Context) error {
	plan := work.lease.Target.Plan
	expected, err := migrationDigests(plan.Candidate)
	if err != nil {
		return err
	}
	value, err := work.driver.inspectContainer(ctx, plan, "migration", work.session.Files())
	if err != nil {
		return err
	}
	if value != nil && value.State.Running {
		return work.waitMigration(ctx, expected)
	}
	history, err := work.session.History(ctx, expected)
	if err != nil {
		return err
	}
	if history.Integrity == "verified" {
		if value != nil && value.State.Status == "exited" && value.State.ExitCode != 0 {
			return servicedeploy.Failure("DEPLOY_MIGRATION_FAILED", "原迁移任务失败，虽然历史可读仍须核实原退出结果", false)
		}
		return work.session.LockMigration(ctx)
	}
	if err := historyFailure(history); err != nil {
		return err
	}
	if value != nil && value.State.Status != "created" {
		return servicedeploy.Failure("DEPLOY_MIGRATION_RESULT_UNKNOWN", "原迁移任务已退出且历史未确认，禁止再次执行DDL", true)
	}
	if err := work.session.UnlockMigration(ctx); err != nil {
		return err
	}
	if err := work.session.Start(ctx, "migration"); err != nil {
		return err
	}
	return work.waitMigration(ctx, expected)
}

// waitMigration 独立期限内只观察原任务，完成后锁迁移账号并核实真实历史。
func (work *execution) waitMigration(ctx context.Context, expected map[uint64]string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(work.lease.Target.Plan.Policy.MigrationSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := work.driver.inspectContainer(ctx, work.lease.Target.Plan, "migration", work.session.Files())
		if err != nil {
			return err
		}
		if value == nil {
			return servicedeploy.Failure("DEPLOY_MIGRATION_RESULT_UNKNOWN", "原迁移任务缺失，禁止通过新任务掩盖结果", true)
		}
		if !value.State.Running {
			return work.finishMigration(ctx, value, expected)
		}
		select {
		case <-ctx.Done():
			return servicedeploy.Failure("DEPLOY_MIGRATION_TIMEOUT", "原迁移仍未核实结束，保留原任务并等待核实", true)
		case <-ticker.C:
		}
	}
}

// finishMigration 退出事实、锁定用途账号和数据库内容分别核实，非零退出不伪报成功。
func (work *execution) finishMigration(ctx context.Context, value *containerState, expected map[uint64]string) error {
	if err := work.session.LockMigration(ctx); err != nil {
		return err
	}
	history, err := work.session.History(ctx, expected)
	if err != nil {
		return err
	}
	if history.Integrity == "dirty" {
		return historyFailure(history)
	}
	if value.State.Status != "exited" || value.State.ExitCode != 0 {
		return servicedeploy.Failure("DEPLOY_MIGRATION_FAILED", "原迁移任务未成功退出，保留任务与数据等待核实", false)
	}
	if history.Integrity != "verified" {
		return servicedeploy.Failure("DEPLOY_MIGRATION_RESULT_UNKNOWN", "迁移任务已结束但版本或内容未确认，禁止重跑DDL", true)
	}
	return nil
}

// historyFailure 只有全新空历史可以启动一次迁移，dirty和部分内容必须由明确核实处理。
func historyFailure(value mysqlruntime.MigrationHistory) error {
	if value.Integrity == "missing" && value.Version == 0 {
		return nil
	}
	if value.Integrity == "dirty" {
		return servicedeploy.Failure("DEPLOY_MIGRATION_DIRTY", "原迁移为dirty，保留原数据；禁止force、清dirty或自动重跑", true)
	}
	return servicedeploy.Failure("DEPLOY_MIGRATION_INTEGRITY_CONFLICT", "原迁移版本或内容证据不完整，禁止覆盖摘要或重复DDL", true)
}
