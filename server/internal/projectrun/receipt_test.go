// 本文件验证回执恢复先于外部检查，网络重试不重发停止命令。
package projectrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/hailingu/twine-realm/server/internal/project"
	"testing"
)

// receiptStore 仅暴露回执读取，其他调用说明重放越过预期边界。
type receiptStore struct {
	Repository
	receipt Receipt
	hash    [32]byte
	err     error
}

// ProjectRunReceipt 返回已持久请求。
func (s *receiptStore) ProjectRunReceipt(context.Context, string) (Receipt, [32]byte, error) {
	return s.receipt, s.hash, s.err
}

// TestReceiptReplayWithoutAdapter 验证禁用适配器后仍能恢复已受理原请求。
func TestReceiptReplayWithoutAdapter(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	command := Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: "stopped"}
	data, err := json.Marshal(struct {
		Project string
		Command Command
	}{id, command})
	if err != nil {
		t.Fatal(err)
	}
	store := &receiptStore{receipt: Receipt{MutationID: id, OperationID: "original", Target: Target{ProjectID: id}}, hash: sha256.Sum256(data)}
	service := NewService(store, nil, nil)
	result, err := service.Accept(context.Background(), id, command)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.OperationID != "original" {
		t.Fatal("原回执未恢复")
	}
	command.Desired = "running"
	if _, err := service.Accept(context.Background(), id, command); err == nil {
		t.Fatal("同键异义未拒绝")
	}
}

// TestReceiptProjectIsolation 不泄露另一项目的原请求。
func TestReceiptProjectIsolation(t *testing.T) {
	service := NewService(&receiptStore{receipt: Receipt{Target: Target{ProjectID: "other"}}}, nil, nil)
	if _, err := service.Receipt(context.Background(), "current", "key"); err == nil {
		t.Fatal("跨项目读取成功")
	}
}

// TestDisabledExecutorRejectsNewCommand 历史读取与新动作启用条件独立。
func TestDisabledExecutorRejectsNewCommand(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	service := NewService(&receiptStore{err: project.Failure("MUTATION_NOT_FOUND", "不存在")}, nil, nil)
	if _, err := service.Accept(context.Background(), id, Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: "running"}); err == nil {
		t.Fatal("未启用执行器却受理")
	}
}
