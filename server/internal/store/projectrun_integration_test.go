//go:build integration

// 本文件用真实元数据库验证父租约、永久未知与项目隔离，不执行Docker动作。
package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
)

// projectRunFixture 仅在门禁提供的一次性数据库内创建随机项目。
func projectRunFixture(t *testing.T) (*Store, projectrun.Receipt) {
	t.Helper()
	dsn := os.Getenv("TWINE_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("必须注入隔离元数据库")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	key, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.Create(context.Background(), project.CreateCommand{MutationID: key, Name: "父账本隔离验证"}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runtimeWrite(context.Background(), s.db, func(tx *sql.Tx) (projectrun.Receipt, error) {
		if err := lockBuildProject(context.Background(), tx, base.Project.ID); err != nil {
			return projectrun.Receipt{}, err
		}
		return insertProjectRun(context.Background(), tx, projectrun.AcceptedInput{ProjectID: base.Project.ID, Command: projectrun.Command{MutationID: mutation, Desired: "stopped"}, Frozen: projectrun.Frozen{ApplicationContainerID: "original", MigrationContainerID: "migration", DatabaseContainerID: "database"}}, "0")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec("UPDATE projectrun_instances SET state='failed' WHERE project_id=?", receipt.Target.ProjectID); err != nil {
			t.Error(err)
		}
	})
	return s, receipt
}

// TestProjectRunUnknownSurvivesReclaim 验证在途动作不可重领或伪装完成。
func TestProjectRunUnknownSurvivesReclaim(t *testing.T) {
	s, receipt := projectRunFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	lease, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || lease == nil {
		t.Fatalf("领取失败: %v", err)
	}
	if lease.Target.ProjectID != receipt.Target.ProjectID {
		t.Fatal("领取错项目")
	}
	ok, err := s.CheckpointProjectRun(ctx, *lease, projectrun.Step{Name: "stop_application", State: "executing", ExternalID: "original"})
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE projectrun_operations SET lease_until=? WHERE id=?", now.Add(-time.Minute), lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || reclaimed != nil {
		t.Fatalf("未知动作被重领: %v", err)
	}
	value, err := s.ProjectRunSnapshot(ctx, receipt.Target.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if value.Operation.State != "needs_attention" || value.Operation.Steps[0].State != "unknown" {
		t.Fatal("未知账本丢失")
	}
	if ok, err := s.ProjectRunAuthorized(ctx, *lease); err != nil || ok {
		t.Fatal("旧执行仍有许可")
	}
	if err := projectRunPermission(ctx, s.db, receipt.Target.ProjectID); err == nil {
		t.Fatal("停止中错误放行组件")
	}
}

// TestProjectRunKnownBoundaryResumes 验证已完成步骤保留，旧执行无法覆盖新租约。
func TestProjectRunKnownBoundaryResumes(t *testing.T) {
	s, receipt := projectRunFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	lease, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	completeTestProjectStep(t, s, *lease, "stop_application", "original")
	if err := s.YieldProjectRun(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if next.Execution <= lease.Execution || next.Operation.Steps[0].State != "succeeded" {
		t.Fatal("已知边界未保持")
	}
	if ok, err := s.CheckpointProjectRun(ctx, *lease, projectrun.Step{Name: "stop_database", State: "executing"}); err != nil || ok {
		t.Fatal("旧执行写入成功")
	}
	epoch, err := projectRunEpoch(ctx, s.db, receipt.Target.ProjectID)
	if err != nil || epoch != "1" {
		t.Fatal("父采样代次错误")
	}
	if err := s.YieldProjectRun(ctx, *next); err != nil {
		t.Fatal(err)
	}
	// 测试只终结自有父操作，避免影响后续队列。
	if _, err := s.db.Exec("UPDATE projectrun_instances SET state='failed' WHERE project_id=?", receipt.Target.ProjectID); err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunOrderAndObservationFence 验证错误物理身份、乱序及完成边界围栏。
func TestProjectRunOrderAndObservationFence(t *testing.T) {
	s, receipt := projectRunFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	lease, err := s.ClaimProjectRun(ctx, now, now.Add(time.Minute))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	for _, step := range []projectrun.Step{{Name: "stop_database", State: "executing", ExternalID: "database"}, {Name: "stop_application", State: "executing", ExternalID: "replacement"}, {Name: "arbitrary", State: "executing", ExternalID: "original"}} {
		if ok, err := s.CheckpointProjectRun(ctx, *lease, step); err == nil || ok {
			t.Fatal("不合法步骤未被拒绝")
		}
	}
	before, err := projectRunEpoch(ctx, s.db, receipt.Target.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	facts := &projectrun.Facts{ApplicationStopped: true, DatabaseStopped: true, ApplicationObservedAt: now.Format(time.RFC3339Nano), DatabaseObservedAt: now.Format(time.RFC3339Nano)}
	if err := s.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "succeeded", Facts: facts}); err == nil {
		t.Fatal("缺少账本却成功")
	}
	for i, name := range []string{"stop_application", "stop_migration", "stop_database"} {
		completeTestProjectStep(t, s, *lease, name, []string{"original", "migration", "database"}[i])
	}
	if err := s.FinishProjectRun(ctx, *lease, projectrun.Completion{State: "succeeded", Facts: facts}); err != nil {
		t.Fatal(err)
	}
	after, err := projectRunEpoch(ctx, s.db, receipt.Target.ProjectID)
	if err != nil || after == before {
		t.Fatal("完成前采样未被独立令牌围栏")
	}
	value, err := s.ProjectStateV6(ctx, receipt.Target.ProjectID)
	if err != nil || value.Lifecycle.Operation.State != "succeeded" || value.State.Cursor == "0" {
		t.Fatal("父状态与通知未一致提交")
	}
}

// completeTestProjectStep 真实提交一项动作前后账本，保留全部成功断言。
func completeTestProjectStep(t *testing.T, s *Store, lease projectrun.Lease, name, id string) {
	t.Helper()
	for _, state := range []string{"executing", "succeeded"} {
		ok, err := s.CheckpointProjectRun(context.Background(), lease, projectrun.Step{Name: name, State: state, ExternalID: id})
		if err != nil || !ok {
			t.Fatal(err)
		}
	}
}
