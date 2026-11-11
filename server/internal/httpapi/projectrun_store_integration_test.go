//go:build integration

// 本文件用真实元数据库验证生命周期围栏，测试事实不冒充Docker验收。
package httpapi_test

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"strings"
	"testing"
	"time"
)

// setParentFence 只在本测试项目内模拟父状态转换，物理验收另行执行。
func setParentFence(t *testing.T, f *runtimeFixture, epoch int, state string) {
	t.Helper()
	_, err := f.sql.Exec(`INSERT INTO projectrun_instances(project_id,generation,observation_epoch,current_operation_id,desired,state) VALUES(?,1,?,?,'running',?) ON DUPLICATE KEY UPDATE observation_epoch=?,state=?`, f.current.ID, epoch, id(t), state, epoch, state)
	if err != nil {
		t.Fatal(err)
	}
}

// TestProjectRunRuntimeObservationEpoch 验证同一组件代次下三个迟到采样窗口。
func TestProjectRunRuntimeObservationEpoch(t *testing.T) {
	f := openRuntimeFixture(t)
	receipt := f.accept(t, f.command)
	ctx := context.Background()
	fact := mysqlruntime.Observation{State: "running", Running: true, Ready: true, ServerUUID: id(t), ContainerID: strings.Repeat("a", 64), VolumeFound: true, ObservedAt: mysqlruntime.Timestamp(time.Now())}
	for _, window := range []struct {
		name           string
		sample, finish int
	}{{"受理前旧采样", 0, 2}, {"执行期间采样晚于完成", 3, 4}, {"原操作重试后旧采样", 4, 5}} {
		t.Run(window.name, func(t *testing.T) {
			target := receipt.Target
			target.ObservationEpoch = stringEpoch(window.sample)
			setParentFence(t, f, window.finish, "succeeded")
			if err := f.base.database.ObserveRuntime(ctx, target, &fact, "", time.Now()); err != nil {
				t.Fatal(err)
			}
			if f.snapshot(t).Observation != nil {
				t.Fatal("迟到采样覆盖父转换")
			}
		})
	}
	setParentFence(t, f, 6, "executing")
	current := receipt.Target
	current.ObservationEpoch = "6"
	if err := f.base.database.ObserveRuntime(ctx, current, &fact, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.snapshot(t).Observation != nil {
		t.Fatal("执行中允许普通观察写入")
	}
	setParentFence(t, f, 7, "succeeded")
	current.ObservationEpoch = "7"
	if err := f.base.database.ObserveRuntime(ctx, current, &fact, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.snapshot(t).Observation == nil {
		t.Fatal("恢复后新采样被拒绝")
	}
}

// stringEpoch 固定测试令牌避免与采样时间混淆。
func stringEpoch(value int) string { return []string{"0", "1", "2", "3", "4", "5", "6", "7"}[value] }

// TestProjectRunRuntimeQueueSkipsBlockedHead 验证被父阻挡的旧头不饿死其他项目。
func TestProjectRunRuntimeQueueSkipsBlockedHead(t *testing.T) {
	blocked := openRuntimeFixture(t)
	blocked.accept(t, blocked.command)
	setParentFence(t, blocked, 1, "executing")
	other := openRuntimeFixture(t)
	other.accept(t, other.command)
	lease := other.claim(t)
	if lease.Target.ProjectID != other.current.ID {
		t.Fatal("阻挡项目饿死其他队列")
	}
	setParentFence(t, other, 1, "executing")
	if err := other.base.database.FinishRuntime(context.Background(), *lease, mysqlruntime.Completion{State: "failed", Code: "TEST_LATE"}); err == nil {
		t.Fatal("父围栏未拒绝旧Finish")
	}
	if other.snapshot(t).Operation.State != "executing" {
		t.Fatal("旧Finish改写组件历史")
	}
}
