// 本文件先恢复原键回执，再准备只读冻结，受理事务仍须重新核实原目标。
package projectrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"strconv"
)

// Reader 提供当前已验证组件目标，不拥有生命周期事务。
type Reader interface {
	RuntimeSnapshot(context.Context, string) (mysqlruntime.Snapshot, error)
	Graph(context.Context, string) (project.GraphSnapshot, error)
	DeploymentSnapshot(context.Context, string, string) (servicedeploy.Snapshot, error)
}

// Service 组合持久受理与显式启用的严格适配器。
type Service struct {
	repository Repository
	reader     Reader
	adapter    Adapter
}

// NewService 不在装配时执行外部动作。
func NewService(repository Repository, reader Reader, adapter Adapter) *Service {
	return &Service{repository, reader, adapter}
}

// Snapshot 只读取持久父状态。
func (s *Service) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	return s.repository.ProjectRunSnapshot(ctx, id)
}

// Receipt 验证回执属于请求项目。
func (s *Service) Receipt(ctx context.Context, id, key string) (Receipt, error) {
	value, _, err := s.repository.ProjectRunReceipt(ctx, key)
	if err == nil && value.Target.ProjectID != id {
		return Receipt{}, project.Failure("MUTATION_NOT_FOUND", "未找到项目原请求")
	}
	return value, err
}

// Accept 原键重复不重新冻结，恢复只使用已停止原目标。
func (s *Service) Accept(ctx context.Context, id string, c Command) (Receipt, error) {
	if err := validate(id, c); err != nil {
		return Receipt{}, err
	}
	data, err := json.Marshal(struct {
		Project string
		Command Command
	}{id, c})
	if err != nil {
		return Receipt{}, err
	}
	hash := sha256.Sum256(data)
	old, previous, err := s.repository.ProjectRunReceipt(ctx, c.MutationID)
	if err == nil {
		if previous != hash {
			return Receipt{}, project.Failure("IDEMPOTENCY_CONFLICT", "原请求键对应不同内容")
		}
		old.Replayed = true
		return old, nil
	}
	var failure *project.Error
	if !errors.As(err, &failure) || failure.Code != "MUTATION_NOT_FOUND" {
		return Receipt{}, err
	}
	if s.adapter == nil {
		return Receipt{}, project.Failure("PROJECT_RUN_DISABLED", "未启用原项目停止恢复")
	}
	frozen, proof, err := s.prepare(ctx, id, c.Desired)
	if err != nil {
		return Receipt{}, err
	}
	return s.repository.AcceptProjectRun(ctx, AcceptedInput{ProjectID: id, Command: c, Frozen: frozen, Proof: proof, Hash: hash})
}

// validate 拒绝无效身份和不明确的目标。
func validate(id string, c Command) error {
	rev, revision := strconv.ParseUint(c.BaseRevision, 10, 64)
	gen, generation := strconv.ParseUint(c.BaseGeneration, 10, 64)
	if !project.ValidID(id) || !project.ValidID(c.MutationID) || revision != nil || generation != nil || strconv.FormatUint(rev, 10) != c.BaseRevision || strconv.FormatUint(gen, 10) != c.BaseGeneration || (c.Desired != "stopped" && c.Desired != "running") {
		return project.Failure("INVALID_REQUEST", "项目生命周期请求无效")
	}
	return nil
}

// prepare 恢复沿用冻结目标；停止只接纳唯一成功部署。
func (s *Service) prepare(ctx context.Context, id, desired string) (Frozen, Facts, error) {
	if desired == "running" {
		value, err := s.repository.ProjectRunSnapshot(ctx, id)
		if err != nil {
			return Frozen{}, Facts{}, err
		}
		if value.Target == nil {
			return Frozen{}, Facts{}, project.Failure("PROJECT_RUN_NOT_STOPPED", "没有已停止原目标")
		}
		return value.Target.Frozen, Facts{}, nil
	}
	runtime, err := s.reader.RuntimeSnapshot(ctx, id)
	if err != nil {
		return Frozen{}, Facts{}, err
	}
	if runtime.Target == nil || runtime.Target.Desired != "running" || runtime.Freshness != "fresh" || runtime.Operation == nil || runtime.Operation.State != "succeeded" || runtime.Observation == nil || !runtime.Observation.Ready {
		return Frozen{}, Facts{}, project.Failure("PROJECT_RUN_BUSY", "原数据库尚未核实就绪")
	}
	graph, err := s.reader.Graph(ctx, id)
	if err != nil {
		return Frozen{}, Facts{}, err
	}
	target, err := s.originalDeployment(ctx, id, graph)
	if err != nil {
		return Frozen{}, Facts{}, err
	}

	frozen, proof, err := s.adapter.Freeze(ctx, *runtime.Target, *target)
	if err != nil {
		return Frozen{}, Facts{}, admissionDiagnostic(err)
	}
	return frozen, proof, nil
}

// Retry 显式续跑已知失败的原操作，不让回执读取隐式重启执行。
func (s *Service) Retry(ctx context.Context, id, key string) (Receipt, error) {
	if !project.ValidID(id) || !project.ValidID(key) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "原请求身份无效")
	}
	if s.adapter == nil {
		return Receipt{}, project.Failure("PROJECT_RUN_DISABLED", "未启用原项目停止恢复")
	}
	return s.repository.RetryProjectRun(ctx, id, key)
}

// originalDeployment 只选择唯一已核实的原部署，不要求未部署草稿产生资源。
func (s *Service) originalDeployment(ctx context.Context, id string, graph project.GraphSnapshot) (*servicedeploy.Target, error) {
	var target *servicedeploy.Target
	for _, node := range graph.Project.Nodes {
		deployment, err := s.reader.DeploymentSnapshot(ctx, id, node.ID)
		if err != nil {
			return nil, err
		}
		if deployment.Target == nil {
			continue
		}
		if target != nil || deployment.Operation == nil || deployment.Operation.State != "succeeded" || deployment.Observation == nil || !deployment.Observation.Ready || deployment.Freshness != "fresh" {
			return nil, project.Failure("PROJECT_RUN_BUSY", "须有唯一已核实原业务部署")
		}
		target = deployment.Target
	}
	if target == nil {
		return nil, project.Failure("PROJECT_RUN_DEPLOYMENT_REQUIRED", "没有原业务部署")
	}
	return target, nil
}

// admissionDiagnostic 仅将受信固定诊断送至HTTP，不暴露底层工具输出或秘密。
func admissionDiagnostic(err error) error {
	code, message, unknown := servicedeploy.Diagnostic(err)
	if unknown {
		return project.Failure("PROJECT_RUN_ADMISSION_UNKNOWN", "原项目主动核实结果未知，未接受新目标，请保留原请求并核实")
	}
	return project.Failure(code, message)
}
