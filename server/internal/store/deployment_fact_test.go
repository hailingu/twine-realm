// 本文件独立验证观测比较边界，实际事务与双采集乱序另有真实SQL回归。
package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// deploymentFactFixture 只读取公开结构样例，不执行样例路径或容器。
func deploymentFactFixture(t *testing.T) (servicedeploy.Target, servicedeploy.Observation) {
	t.Helper()
	data, err := os.ReadFile("../../contract/v4/fixtures/preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan servicedeploy.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	fact := servicedeploy.Observation{State: "ready", ContainerID: strings.Repeat("a", 64), ImageID: plan.Candidate.Image.ID, Running: true, Healthy: true, Ready: true, DatabaseUUID: plan.Database.ServerUUID, MigrationVersion: 2, MigrationIntegrity: "verified", EntryURL: "http://127.0.0.1:8081", ObservedAt: mysqlruntime.Timestamp(time.Now().UTC())}
	return servicedeploy.Target{Plan: plan}, fact
}

// TestDeploymentObservationOrdering 旧事实、旧失败、损坏时间和跨资源身份都不能改变当前投影。
func TestDeploymentObservationOrdering(t *testing.T) {
	target, fact := deploymentFactFixture(t)
	target.Plan.Request.HostPort = 8081
	at := time.Now().UTC()
	fact.ObservedAt = mysqlruntime.Timestamp(at)
	data, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	instance := deploymentInstance{lastAttempt: sql.NullTime{Time: at, Valid: true}, observation: data}
	old := fact
	old.ObservedAt = mysqlruntime.Timestamp(at.Add(-time.Second))
	for _, incoming := range []*servicedeploy.Observation{&old, nil} {
		current, err := currentDeploymentFact(&instance, target, incoming, at.Add(-time.Second))
		if err != nil || current {
			t.Fatal("旧采集或失败未被忽略", err)
		}
	}
	if current, err := currentDeploymentFact(&instance, target, &old, at.Add(time.Second)); err != nil || current {
		t.Fatal("稍后提交的旧采集未被忽略", err)
	}
	if current, err := currentDeploymentFact(&instance, target, &fact, at); err != nil || !current {
		t.Fatal("一致新事实被丢弃", err)
	}
	for _, change := range []func(*servicedeploy.Observation){
		func(v *servicedeploy.Observation) { v.ImageID = "sha256:" + strings.Repeat("f", 64) },
		func(v *servicedeploy.Observation) { v.DatabaseUUID = "foreign" },
		func(v *servicedeploy.Observation) { v.EntryURL = "http://0.0.0.0:8081" },
		func(v *servicedeploy.Observation) { v.ObservedAt = "not-a-time" },
	} {
		value := fact
		change(&value)
		if _, err := currentDeploymentFact(&instance, target, &value, at); err == nil {
			t.Fatal("非法或跨资源观测被接受")
		}
	}
	instance.observation = []byte("{")
	if _, err := currentDeploymentFact(&instance, target, &fact, at); err == nil {
		t.Fatal("损坏原事实被静默覆盖")
	}
}
