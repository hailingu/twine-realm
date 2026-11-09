// 本文件覆盖受理准备的存储故障与非就绪边界，确保不进入适配器。
package projectrun

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"testing"
)

// errorReader 可分别在三个只读边界返回错误，不伪造写入结果。
type errorReader struct {
	runtime                             mysqlruntime.Snapshot
	graph                               project.GraphSnapshot
	deployment                          servicedeploy.Snapshot
	runtimeErr, graphErr, deploymentErr error
}

// RuntimeSnapshot 返回指定数据库准备结果。
func (r errorReader) RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error) {
	return r.runtime, r.runtimeErr
}

// Graph 返回指定画布读取结果。
func (r errorReader) Graph(context.Context, string) (project.GraphSnapshot, error) {
	return r.graph, r.graphErr
}

// DeploymentSnapshot 返回指定原部署读取结果。
func (r errorReader) DeploymentSnapshot(context.Context, string, string) (servicedeploy.Snapshot, error) {
	return r.deployment, r.deploymentErr
}

// readyErrorReader 提供最小已核实准备前置，测试逐项破坏。
func readyErrorReader() errorReader {
	return errorReader{runtime: mysqlruntime.Snapshot{Target: &mysqlruntime.Target{Desired: "running"}, Operation: &mysqlruntime.Operation{State: "succeeded"}, Observation: &mysqlruntime.Observation{Ready: true}, Freshness: "fresh"}, graph: project.GraphSnapshot{Project: project.Project{Nodes: []project.Node{{ID: "one"}}}}, deployment: servicedeploy.Snapshot{Target: &servicedeploy.Target{}, Operation: &servicedeploy.Operation{State: "succeeded"}, Observation: &servicedeploy.Observation{Ready: true}, Freshness: "fresh"}}
}

// TestPrepareRejectsUnavailableComponents 各只读失败和未就绪均在外部冻结前拒绝。
func TestPrepareRejectsUnavailableComponents(t *testing.T) {
	failure := errors.New("读取失败")
	cases := []struct {
		name   string
		change func(*errorReader)
	}{
		{"数据库读取失败", func(r *errorReader) { r.runtimeErr = failure }},
		{"无数据库目标", func(r *errorReader) { r.runtime.Target = nil }},
		{"数据库不是运行目标", func(r *errorReader) { r.runtime.Target.Desired = "stopped" }},
		{"数据库过期", func(r *errorReader) { r.runtime.Freshness = "stale" }},
		{"数据库未成功", func(r *errorReader) { r.runtime.Operation.State = "executing" }},
		{"数据库无观察", func(r *errorReader) { r.runtime.Observation = nil }},
		{"数据库不就绪", func(r *errorReader) { r.runtime.Observation.Ready = false }},
		{"画布读取失败", func(r *errorReader) { r.graphErr = failure }},
		{"部署读取失败", func(r *errorReader) { r.deploymentErr = failure }},
		{"无部署目标", func(r *errorReader) { r.deployment.Target = nil }},
		{"两个原部署", func(r *errorReader) { r.graph.Project.Nodes = append(r.graph.Project.Nodes, project.Node{ID: "two"}) }},
		{"部署未成功", func(r *errorReader) { r.deployment.Operation.State = "failed" }},
		{"部署无观察", func(r *errorReader) { r.deployment.Observation = nil }},
		{"部署不就绪", func(r *errorReader) { r.deployment.Observation.Ready = false }},
		{"部署过期", func(r *errorReader) { r.deployment.Freshness = "stale" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := readyErrorReader()
			tc.change(&r)
			if _, _, err := NewService(nil, r, nil).prepare(context.Background(), "project", "stopped"); err == nil {
				t.Fatal("未就绪边界被接受")
			}
		})
	}
}

// stateStore 提供原父快照与重试的明确持久结果。
type stateStore struct {
	Repository
	value   Snapshot
	failure error
	retried bool
}

// ProjectRunSnapshot 返回指定原父状态或存储错误。
func (s *stateStore) ProjectRunSnapshot(context.Context, string) (Snapshot, error) {
	return s.value, s.failure
}

// RetryProjectRun 记录请求已明确到达持久重试端口。
func (s *stateStore) RetryProjectRun(context.Context, string, string) (Receipt, error) {
	s.retried = true
	return Receipt{OperationID: "original"}, s.failure
}

// TestRestoreRequiresOriginalSnapshot 没有原目标不能冻结新资源替代恢复。
func TestRestoreRequiresOriginalSnapshot(t *testing.T) {
	failure := errors.New("读取失败")
	store := &stateStore{failure: failure}
	service := NewService(store, nil, nil)
	if _, err := service.Snapshot(context.Background(), "project"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, _, err := service.prepare(context.Background(), "project", "running"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	store.failure = nil
	if _, _, err := service.prepare(context.Background(), "project", "running"); err == nil {
		t.Fatal("无原目标仍恢复")
	}
	store.value.Target = &Target{Frozen: Frozen{ServerUUID: "original"}}
	frozen, proof, err := service.prepare(context.Background(), "project", "running")
	if err != nil || frozen.ServerUUID != "original" || proof.Runtime != nil {
		t.Fatal("恢复替换冻结身份或虚构证明", err)
	}
}

// TestRetryBoundaries 无效身份及禁用执行器不得到达存储重试端口。
func TestRetryBoundaries(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	store := &stateStore{}
	service := NewService(store, nil, nil)
	if _, err := service.Retry(context.Background(), "invalid", id); err == nil {
		t.Fatal("无效身份被接受")
	}
	if _, err := service.Retry(context.Background(), id, id); err == nil || store.retried {
		t.Fatal("禁用仍重试")
	}
	service.adapter = proofAdapter{}
	receipt, err := service.Retry(context.Background(), id, id)
	if err != nil || !store.retried || receipt.OperationID != "original" {
		t.Fatal("原重试未传递", err)
	}
}

// TestReceiptAndAcceptStorageFailures 原键读取失败不能被误判成新请求。
func TestReceiptAndAcceptStorageFailures(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	failure := errors.New("存储故障")
	service := NewService(&receiptStore{err: failure}, nil, nil)
	if _, err := service.Receipt(context.Background(), id, id); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	command := Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: "stopped"}
	if _, err := service.Accept(context.Background(), id, command); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	command.BaseRevision = "bad"
	if _, err := service.Accept(context.Background(), id, command); err == nil {
		t.Fatal("无效版本未拒绝")
	}
	service = NewService(&receiptStore{err: project.Failure("MUTATION_NOT_FOUND", "不存在")}, errorReader{runtimeErr: failure}, proofAdapter{})
	command.BaseRevision = "1"
	if _, err := service.Accept(context.Background(), id, command); !errors.Is(err, failure) {
		t.Fatal("准备失败被吞掉", err)
	}
}
