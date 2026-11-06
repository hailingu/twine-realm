//go:build integration

// 本文件验证同一原目标的采集也必须单调，迟到事实与失败不能覆盖较新采样。
package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// assertMonotonicDeploymentFacts 使用真实元数据库模拟两个采集者提交乱序，不修改实际运行资源。
func assertMonotonicDeploymentFacts(t *testing.T, f *deploymentFixture, receipt servicedeploy.Receipt, fact *servicedeploy.Observation) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Microsecond)
	newer := *fact
	newer.ObservedAt = mysqlruntime.Timestamp(at)
	store := f.runtime.base.database
	if err := store.ObserveDeployment(context.Background(), receipt.Target, &newer, "", at); err != nil {
		t.Fatal(err)
	}
	older := newer
	older.State, older.Ready, older.Running, older.Healthy, older.EntryURL = "stopped", false, false, false, ""
	older.ObservedAt = mysqlruntime.Timestamp(at.Add(-time.Second))
	if err := store.ObserveDeployment(context.Background(), receipt.Target, &older, "", at.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	value := f.snapshot(t)
	if value.Observation == nil || !value.Observation.Ready || value.Observation.ObservedAt != newer.ObservedAt {
		t.Fatal("同目标迟到采集覆盖了较新的就绪事实")
	}
	if err := store.ObserveDeployment(context.Background(), receipt.Target, nil, "DEPLOY_RESULT_UNKNOWN", at.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	value = f.snapshot(t)
	if value.ObservationCode != "" || value.Observation == nil || !value.Observation.Ready {
		t.Fatal("迟到失败覆盖了较新采样")
	}
}
