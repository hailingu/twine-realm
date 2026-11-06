// 本文件将只读预览与同库受理分开；浏览器断连不拥有部署进程生命周期。
package servicedeploy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Service 组合受理、查询和显式本机适配器，未启用仍保留历史。
type Service struct {
	repository   Repository
	declarations DeclarationReader
	adapter      Adapter
}

// NewService 不在装配时执行Docker或初始化业务数据库。
func NewService(repository Repository, declarations DeclarationReader, adapter Adapter) *Service {
	return &Service{repository, declarations, adapter}
}

// Capability 默认禁用且满足真实机器契约；配置不等于一次部署已通过。
func (service *Service) Capability() Capability {
	value := Capability{SchemaVersion: 1, Adapter: "disabled", Code: "DEPLOY_DISABLED", Message: "未启用业务部署，画布、构建和历史仍可使用", Policy: DefaultPolicy()}
	if service.adapter != nil {
		value.Enabled, value.Adapter, value.Code, value.Message = true, "go-mysql-compose-v1", "CONFIGURED", "已配置单服务MySQL部署；执行时另核实候选、归属与真实迁移就绪"
	}
	return value
}

// Preview 只读取当前声明、候选、真实MySQL和镜像，绝不建库、生成密码或创建容器。
func (service *Service) Preview(ctx context.Context, id, serviceID string, request PreviewRequest) (Plan, error) {
	if err := ValidatePreview(id, serviceID, request); err != nil {
		return Plan{}, err
	}
	if service.adapter == nil {
		return Plan{}, project.Failure("DEPLOY_DISABLED", "未启用业务部署，未接受目标")
	}
	graph, err := service.declarations.Graph(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	build, err := service.declarations.BuildSnapshot(ctx, id, serviceID)
	if err != nil {
		return Plan{}, err
	}
	runtime, err := service.declarations.RuntimeSnapshot(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	database, environment, err := service.adapter.Prepare(ctx, runtime, build)
	if err != nil {
		return Plan{}, domainError(err)
	}
	return MakePlan(graph, build, request, database, environment)
}

// Accept 首先恢复原键已提交结果，再重验用户审阅计划，未提交前不执行任何部署副作用。
func (service *Service) Accept(ctx context.Context, id, serviceID string, command Command) (Receipt, error) {
	if err := ValidatePreview(id, serviceID, command.Preview); err != nil {
		return Receipt{}, err
	}
	if !project.ValidID(command.MutationID) || command.RetryOf != "" && !project.ValidID(command.RetryOf) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "部署请求或原操作身份无效")
	}
	data, err := json.Marshal(struct {
		Project, Service string
		Command          Command
	}{id, serviceID, command})
	if err != nil {
		return Receipt{}, err
	}
	hash := sha256.Sum256(data)
	value, stored, err := service.repository.DeploymentReceipt(ctx, command.MutationID)
	var failure *project.Error
	if err == nil {
		if stored != hash {
			return value, project.Failure("IDEMPOTENCY_CONFLICT", "相同部署请求身份已用于不同内容")
		}
		value.Replayed = true
		return value, nil
	}
	if !errors.As(err, &failure) || failure.Code != "MUTATION_NOT_FOUND" {
		return value, err
	}
	plan, err := service.Preview(ctx, id, serviceID, command.Preview)
	if err != nil {
		return value, err
	}
	if plan.Digest != command.PlanDigest {
		return value, project.Failure("DEPLOY_PLAN_CHANGED", "预览已改变，请重新审阅并确认部署")
	}
	return service.repository.AcceptDeployment(ctx, AcceptedInput{Plan: plan, Command: command, Hash: hash})
}

// Receipt 保持原键核实，即使后来禁用适配器或Docker不可达也不重发副作用。
func (service *Service) Receipt(ctx context.Context, id string) (Receipt, error) {
	if !project.ValidID(id) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "部署请求身份无效")
	}
	value, _, err := service.repository.DeploymentReceipt(ctx, id)
	value.Replayed = err == nil
	return value, err
}

// Snapshot 只读一致历史，最后成功与当前运行新鲜度分开。
func (service *Service) Snapshot(ctx context.Context, id, serviceID string) (Snapshot, error) {
	if !project.ValidID(id) || !project.ValidID(serviceID) {
		return Snapshot{}, project.Failure("INVALID_REQUEST", "项目或服务身份无效")
	}
	return service.repository.DeploymentSnapshot(ctx, id, serviceID)
}

// Operation 保留原代次结果与检查点。
func (service *Service) Operation(ctx context.Context, id, serviceID, operationID string) (Operation, error) {
	if !project.ValidID(id) || !project.ValidID(serviceID) || !project.ValidID(operationID) {
		return Operation{}, project.Failure("INVALID_REQUEST", "项目、服务或部署操作身份无效")
	}
	return service.repository.DeploymentOperation(ctx, id, serviceID, operationID)
}
