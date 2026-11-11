// 本文件验证受理携带主动证明而非以被动缓存代替冻结检查。
package projectrun

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"testing"
)

// proofReader 只提供准备阶段所需的已成功原目标。
type proofReader struct{ Reader }

// RuntimeSnapshot 返回准备阶段的旧缓存，不充当主动证明。
func (proofReader) RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error) {
	return mysqlruntime.Snapshot{Target: &mysqlruntime.Target{Desired: "running"}, Operation: &mysqlruntime.Operation{State: "succeeded"}, Observation: &mysqlruntime.Observation{Ready: true, ObservedAt: "cached"}, Freshness: "fresh"}, nil
}

// Graph 指定唯一业务部署节点。
func (proofReader) Graph(context.Context, string) (project.GraphSnapshot, error) {
	return project.GraphSnapshot{Project: project.Project{Nodes: []project.Node{{ID: "service"}}}}, nil
}

// DeploymentSnapshot 返回准备阶段的已成功部署。
func (proofReader) DeploymentSnapshot(context.Context, string, string) (servicedeploy.Snapshot, error) {
	return servicedeploy.Snapshot{Target: &servicedeploy.Target{}, Operation: &servicedeploy.Operation{State: "succeeded"}, Observation: &servicedeploy.Observation{Ready: true}, Freshness: "fresh"}, nil
}

// proofAdapter 在冻结结束时提供独立新证明。
type proofAdapter struct{ Adapter }

// Freeze 不从输入缓存复制主动认证时间。
func (proofAdapter) Freeze(context.Context, mysqlruntime.Target, servicedeploy.Target) (Frozen, Facts, error) {
	return Frozen{ServerUUID: "original"}, Facts{Runtime: &mysqlruntime.Observation{ObservedAt: "active-proof"}}, nil
}

// proofStore 捕获同库受理输入，验证证明不进入永久冻结目标。
type proofStore struct {
	Repository
	input AcceptedInput
}

// ProjectRunReceipt 表示这是尚未受理的新请求。
func (*proofStore) ProjectRunReceipt(context.Context, string) (Receipt, [32]byte, error) {
	return Receipt{}, [32]byte{}, project.Failure("MUTATION_NOT_FOUND", "不存在")
}

// AcceptProjectRun 仅记录传递边界，不代替真实数据库验证测试。
func (s *proofStore) AcceptProjectRun(_ context.Context, input AcceptedInput) (Receipt, error) {
	s.input = input
	return Receipt{}, nil
}

// TestActiveProofForwarded 确认冻结的新证明原样进入事务，旧缓存时间不被误用。
func TestActiveProofForwarded(t *testing.T) {
	repository := &proofStore{}
	service := NewService(repository, proofReader{}, proofAdapter{})
	id := "11111111-1111-4111-8111-111111111111"
	_, err := service.Accept(context.Background(), id, Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.input.Proof.Runtime == nil || repository.input.Proof.Runtime.ObservedAt != "active-proof" || repository.input.Frozen.ServerUUID != "original" {
		t.Fatal("主动证明未独立传入受理")
	}
}
