// 本文件组合固定源码、工程包和同库受理，不在HTTP连接内运行构建。
package servicebuild

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Service 依赖显式端口，账号与业务运行凭据均不属于该切片。
type Service struct {
	commands Commands
	queries  Queries
	sources  SourceReader
	verifier CommitVerifier
	driver   *Driver
	catalog  *engineering.Catalog
}

// NewService 核实内置工程包后装配；未配置执行器仍可查询持久历史。
func NewService(commands Commands, queries Queries, sources SourceReader, verifier CommitVerifier, driver *Driver) (*Service, error) {
	catalog, err := engineering.Load()
	if err != nil {
		return nil, err
	}
	return &Service{commands, queries, sources, verifier, driver, catalog}, nil
}

// Capability 如实报告显式配置和固定支持组合，不把配置当成构建已通过。
func (service *Service) Capability() Capability {
	value := Capability{SchemaVersion: 1, Adapter: "disabled", Code: "BUILD_DISABLED", Message: "未启用本机隔离构建适配器，源码、画布与历史构建仍可查询", Profile: service.catalog.Identity(), ImageReference: ToolchainImage, Policy: DefaultPolicy()}
	if service.driver != nil {
		value.Enabled, value.Adapter, value.Code, value.Message = true, "local-go-container-v1", "CONFIGURED", "已配置固定Go工具链；每次构建另核实实际环境与检查结果"
	}
	return value
}

// Accept 先重放原键，随后核实当前源码与环境，仅持久接受目标。
func (service *Service) Accept(ctx context.Context, id, serviceID string, command Command) (Receipt, error) {
	if !validIDs(id, serviceID, command.MutationID) || !number(command.BaseRevision) || !number(command.BaseGeneration) || !commitPattern.MatchString(command.Commit) || command.RetryOf != "" && !project.ValidID(command.RetryOf) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "构建身份、版本或完整commit无效")
	}
	hash, err := requestHash("build", id, serviceID, "", command)
	if err != nil {
		return Receipt{}, err
	}
	if value, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return value, err
	}
	if service.driver == nil {
		return Receipt{}, project.Failure("BUILD_DISABLED", "未配置本机隔离构建适配器，未接受目标")
	}
	if command.Profile != service.catalog.Identity() {
		return Receipt{}, project.Failure("BUILD_PROFILE_UNSUPPORTED", "请选择当前已发布且完整匹配的工程方案身份")
	}
	source, err := service.sources.Source(ctx, id, serviceID)
	if err != nil {
		return Receipt{}, err
	}
	if source.ProjectRevision != command.BaseRevision {
		return Receipt{}, &project.Error{Code: "REVISION_CONFLICT", Message: "画布已改变，请载入最新保存版本再构建", CurrentRevision: source.ProjectRevision}
	}
	if source.PinnedCommit == nil || *source.PinnedCommit != command.Commit {
		return Receipt{}, project.Failure("BUILD_SOURCE_CHANGED", "构建仅使用此服务已保存的固定commit")
	}
	if err := service.verifier.VerifyCommit(ctx, source.RepositoryPath, command.Commit); err != nil {
		return Receipt{}, err
	}
	if err := service.driver.Preflight(ctx); err != nil {
		return Receipt{}, domainError(err)
	}
	target, err := service.target(source)
	if err != nil {
		return Receipt{}, err
	}
	return service.commands.AcceptBuild(ctx, AcceptedInput{Target: target, Command: command, Hash: hash})
}

// target 将已核实输入转为不可变对象，数据库负责分配服务单调generation。
func (service *Service) target(source project.SourceVersion) (Target, error) {
	id, err := project.NewID()
	if err != nil {
		return Target{}, err
	}
	value := Target{ID: id, ProjectID: source.ProjectID, ServiceID: source.ServiceID, CanvasRevision: source.ProjectRevision, InstallationID: service.driver.InstallationID(), Profile: service.catalog.Identity(), Environment: service.driver.Environment(), Policy: DefaultPolicy(), CreatedAt: Timestamp(time.Now())}
	value.Source = Source{source.ProjectID, source.ProjectRevision, source.ServiceID, source.RepositoryID, source.RepositoryPath, *source.PinnedCommit}
	value.InputDigest, err = InputFingerprint(value.Source, value.Profile, value.Environment, value.Policy, value.InstallationID)
	return value, err
}

// Cancel 使用独立原键表达明确取消，不把浏览器离开解释为取消。
func (service *Service) Cancel(ctx context.Context, id, serviceID, operationID string, command CancelCommand) (Receipt, error) {
	if !validIDs(id, serviceID, operationID, command.MutationID) || !number(command.BaseVersion) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "取消身份或操作版本无效")
	}
	hash, err := requestHash("cancelBuild", id, serviceID, operationID, command)
	if err != nil {
		return Receipt{}, err
	}
	if value, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return value, err
	}
	if service.driver == nil {
		return Receipt{}, project.Failure("BUILD_DISABLED", "恢复原构建适配器后才能核实并取消原沙箱")
	}
	return service.commands.CancelBuild(ctx, id, serviceID, operationID, command, hash)
}

// Receipt 可在执行器关闭时恢复原请求，不重复受理或创建资源。
func (service *Service) Receipt(ctx context.Context, id string) (Receipt, error) {
	if !project.ValidID(id) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "构建请求身份无效")
	}
	value, _, err := service.commands.BuildReceipt(ctx, id)
	value.Replayed = err == nil
	return value, err
}

// replay 对相同意图返回原结果；执行器或源码后来失联不影响原键核实。
func (service *Service) replay(ctx context.Context, id string, hash [32]byte) (Receipt, bool, error) {
	value, stored, err := service.commands.BuildReceipt(ctx, id)
	var domain *project.Error
	if errors.As(err, &domain) && domain.Code == "MUTATION_NOT_FOUND" {
		return Receipt{}, false, nil
	}
	if err != nil {
		return value, false, err
	}
	if hash != stored {
		return Receipt{}, true, project.Failure("IDEMPOTENCY_CONFLICT", "相同构建请求身份已用于不同意图")
	}
	value.Replayed = true
	return value, true, nil
}

// Operation 只读取原操作；新源码或新构建不会覆盖旧诊断。
func (service *Service) Operation(ctx context.Context, id, serviceID, operationID string) (Operation, error) {
	if !validIDs(id, serviceID, operationID) {
		return Operation{}, project.Failure("INVALID_REQUEST", "构建操作归属身份无效")
	}
	return service.queries.BuildOperation(ctx, id, serviceID, operationID)
}

// Snapshot 保留历史候选并单独核实当前输入、源对象和镜像内容是否仍匹配。
func (service *Service) Snapshot(ctx context.Context, id, serviceID string) (Snapshot, error) {
	if !validIDs(id, serviceID) {
		return Snapshot{}, project.Failure("INVALID_REQUEST", "构建服务身份无效")
	}
	value, err := service.queries.BuildSnapshot(ctx, id, serviceID)
	if err != nil || value.Candidate == nil {
		return value, err
	}
	value.CandidateCode = service.candidateCode(ctx, value)
	value.CandidateCurrent = value.CandidateCode == "CURRENT_INPUT"
	return value, nil
}

// candidateCode 把失效证据与历史成功分开，布局revision本身不改变源码证明。
func (service *Service) candidateCode(ctx context.Context, value Snapshot) string {
	if service.driver == nil {
		return "BUILD_DISABLED"
	}
	source, candidate := value.CurrentSource, value.Candidate
	if source.PinnedCommit == nil || value.Operation == nil || value.Operation.State != "succeeded" || value.Operation.ID != candidate.OperationID {
		return "HISTORICAL_CANDIDATE"
	}
	current := Source{source.ProjectID, source.ProjectRevision, source.ServiceID, source.RepositoryID, source.RepositoryPath, *source.PinnedCommit}
	digest, err := InputFingerprint(current, service.catalog.Identity(), service.driver.Environment(), DefaultPolicy(), service.driver.InstallationID())
	if err != nil || digest != candidate.InputDigest {
		return "INPUT_CHANGED"
	}
	if service.verifier.VerifyCommit(ctx, source.RepositoryPath, *source.PinnedCommit) != nil {
		return "BUILD_SOURCE_UNAVAILABLE"
	}
	if service.driver.Preflight(ctx) != nil {
		return "BUILD_ENVIRONMENT_UNCONFIRMED"
	}
	target := Target{ID: candidate.TargetID, ProjectID: value.ProjectID, InstallationID: service.driver.InstallationID(), InputDigest: candidate.InputDigest, Environment: candidate.Environment}
	if service.driver.verifyImage(ctx, service.driver.newSandbox(target, candidate.OperationID), candidate.Image) != nil {
		return "BUILD_IMAGE_UNCONFIRMED"
	}
	return "CURRENT_INPUT"
}

// requestHash 包含路由与完整意图，防止同键跨服务或改变动作。
func requestHash(kind, id, serviceID, operationID string, command any) ([32]byte, error) {
	data, err := json.Marshal(struct {
		Kind, ProjectID, ServiceID, OperationID string
		Command                                 any
	}{kind, id, serviceID, operationID, command})
	return sha256.Sum256(data), err
}

// validIDs 在所有公开读写入口核实完整UUID，而不是交给SQL猜测。
func validIDs(values ...string) bool {
	for _, value := range values {
		if !project.ValidID(value) {
			return false
		}
	}
	return true
}

// number 限制十进制版本，不能以空值或溢出值绕过并发控制。
func number(value string) bool {
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil && value != ""
}
