// 本文件验证原键优先于当前执行器与预览变化，错误诊断不携带SQL、日志或秘密。
package servicedeploy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// receiptRepository 模拟已提交原键，其他存储入口不属于此纯用例验证。
type receiptRepository struct {
	Repository
	value Receipt
	hash  [32]byte
	err   error
}

// DeploymentReceipt 仅恢复固定原结果，不推断外部执行成功。
func (repository receiptRepository) DeploymentReceipt(context.Context, string) (Receipt, [32]byte, error) {
	return repository.value, repository.hash, repository.err
}

// TestOriginalDeploymentReceiptSurvivesDisabled 关闭执行器后同键仍恢复原受理，不允许异意图复用原键。
func TestOriginalDeploymentReceiptSurvivesDisabled(t *testing.T) {
	p := planFixture(t)
	mutation, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{MutationID: mutation, Preview: p.Request, PlanDigest: p.Digest}
	data, err := json.Marshal(struct {
		Project, Service string
		Command          Command
	}{p.ProjectID, p.ServiceID, command})
	if err != nil {
		t.Fatal(err)
	}
	repository := receiptRepository{value: Receipt{SchemaVersion: 1, MutationID: mutation, OperationID: p.Candidate.OperationID}, hash: sha256.Sum256(data)}
	service := NewService(repository, nil, nil)
	value, err := service.Accept(context.Background(), p.ProjectID, p.ServiceID, command)
	if err != nil || !value.Replayed || value.OperationID != repository.value.OperationID {
		t.Fatal("禁用后原键没有恢复", err)
	}
	command.Preview.HostPort++
	if _, err := service.Accept(context.Background(), p.ProjectID, p.ServiceID, command); err == nil {
		t.Fatal("异意图复用了原部署键")
	}
	if _, err := service.Preview(context.Background(), p.ProjectID, p.ServiceID, p.Request); err == nil {
		t.Fatal("禁用后仍能产生新部署预览")
	}
	for _, id := range []string{"", "invalid"} {
		if _, err := service.Receipt(context.Background(), id); err == nil {
			t.Fatal("非法原键进入查询")
		}
		if _, err := service.Snapshot(context.Background(), id, p.ServiceID); err == nil {
			t.Fatal("非法项目进入查询")
		}
		if _, err := service.Operation(context.Background(), p.ProjectID, p.ServiceID, id); err == nil {
			t.Fatal("非法操作进入查询")
		}
	}
	if value, err := service.Receipt(context.Background(), mutation); err != nil || !value.Replayed {
		t.Fatal("只读原键不可恢复", err)
	}
}

// TestDeploymentDiagnosticKeepsOnlySafeCodes 不同边界错误均保持安全分类，未知原文不能进入事件。
func TestDeploymentDiagnosticKeepsOnlySafeCodes(t *testing.T) {
	cases := []error{Failure("DEPLOY_UNKNOWN", "明确等待核实", true), &localdocker.Fault{Code: "DOCKER_WARNING", Message: "受控工具告警", Unknown: true, Output: []byte("synthetic-private")}, &mysqlruntime.Fault{Code: "MYSQL_NOT_READY", Message: "尚未认证"}, &servicebuild.Fault{Code: "BUILD_DISABLED", Message: "构建未启用"}, &privatefiles.Error{Code: "LOCK_BUSY", Message: "原锁忙"}, project.Failure("INVALID_REQUEST", "请求无效"), errors.New("synthetic-private")}
	for _, err := range cases {
		code, message, _ := Diagnostic(err)
		if code == "" || message == "" || strings.Contains(message, "synthetic-private") {
			t.Fatal("安全诊断边界失效")
		}
		var domain *project.Error
		if !errors.As(domainError(err), &domain) {
			t.Fatal("外部错误未映射为领域错误")
		}
	}
}
